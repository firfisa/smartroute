#!/usr/bin/env ruby
# frozen_string_literal: true

require "digest"
require "fileutils"
require "json"
require "open3"
require "optparse"
require "socket"
require "time"

options = {}
OptionParser.new do |parser|
  parser.on("--source PATH") { |value| options[:source] = value }
  parser.on("--output PATH") { |value| options[:output] = value }
end.parse!

abort "error: --source and --output are required" unless options.values_at(:source, :output).all?

source = File.realpath(options[:source])
output = File.expand_path(options[:output])
abort "error: source runtime must be private" unless File.directory?(source) && File.stat(source).mode & 0o077 == 0
abort "error: output already exists" if File.exist?(output)
abort "error: output must not be inside the source" if output.start_with?(source + File::SEPARATOR)

config_path = File.join(source, "config.json")
runbook_path = File.join(source, "private-runbook.json")
manifest_path = File.join(source, "manifest.json")
abort "error: incomplete source runtime" unless [config_path, runbook_path, manifest_path].all? { |path| File.file?(path) }

config = JSON.parse(File.read(config_path))
runbook = JSON.parse(File.read(runbook_path))
manifest = JSON.parse(File.read(manifest_path))
abort "error: unsupported source runtime schema" unless runbook["schema_version"] == 1 && manifest["schema_version"] == 1

def split_address(value)
  host, port = value.to_s.split(":", 2)
  abort "error: invalid runtime address" unless host == "127.0.0.1" && port&.match?(/\A\d+\z/)

  [host, Integer(port, 10)]
end

[config.fetch("listen_address"), config.fetch("guard_listen_address")].each do |address|
  host, port = split_address(address)
  socket = TCPServer.new(host, port)
  socket.close
rescue Errno::EADDRINUSE, Errno::EACCES
  abort "error: source Supervisor must be stopped before relocation"
end

def relocate_value(value, source, output)
  case value
  when Hash
    value.transform_values { |nested| relocate_value(nested, source, output) }
  when Array
    value.map { |nested| relocate_value(nested, source, output) }
  when String
    if value == source
      output
    elsif value.start_with?(source + File::SEPARATOR)
      output + value.delete_prefix(source)
    else
      value
    end
  else
    value
  end
end

created = false
begin
  FileUtils.mkdir_p(File.dirname(output), mode: 0o700)
  FileUtils.cp_r(source, output, preserve: true)
  created = true
  File.chmod(0o700, output)

  relocated_config = relocate_value(config, source, output)
  relocated_runbook = relocate_value(runbook, source, output)
  relocated_manifest = relocate_value(manifest, source, output)
  relocated_manifest["relocated_at"] = Time.now.utc.iso8601
  relocated_manifest["relocated_runtime"] = true

  new_config = File.join(output, "config.json")
  new_runbook = File.join(output, "private-runbook.json")
  new_manifest = File.join(output, "manifest.json")
  File.write(new_config, JSON.pretty_generate(relocated_config) + "\n", mode: "wb")
  File.write(new_runbook, JSON.pretty_generate(relocated_runbook) + "\n", mode: "wb")
  relocated_manifest["config_sha256"] = Digest::SHA256.file(new_config).hexdigest
  File.write(new_manifest, JSON.pretty_generate(relocated_manifest) + "\n", mode: "wb")
  [new_config, new_runbook, new_manifest].each { |path| File.chmod(0o600, path) }

  Dir.glob(File.join(output, "service", "*.plist")).each { |path| File.delete(path) }
  binary = File.join(output, "bin", "smartroute")
  _, validate_error, validate_status = Open3.capture3(binary, "validate", "-config", new_config)
  abort "error: relocated config is invalid: #{validate_error.strip}" unless validate_status.success?
  _, status_error, status_status = Open3.capture3(binary, "learning", "status", "-config", new_config)
  abort "error: relocated learning store is invalid: #{status_error.strip}" unless status_status.success?

  unsafe = Dir.glob(File.join(output, "**", "*"), File::FNM_DOTMATCH).any? do |path|
    next false if [".", ".."].include?(File.basename(path))

    File.stat(path).mode & 0o077 != 0
  end
  abort "error: relocated runtime contains non-private entries" if unsafe

  puts JSON.generate({
    "relocated" => true,
    "output" => output,
    "config_valid" => true,
    "learning_store_valid" => true,
    "source_stopped" => true,
    "clash_files_read" => false,
    "clash_files_written" => false,
    "clash_reloaded" => false
  })
rescue StandardError, SystemExit => error
  FileUtils.remove_entry_secure(output) if created && File.exist?(output)
  warn(error.is_a?(SystemExit) ? "error: live runtime relocation failed" : "error: #{error.message}")
  exit 1
end
