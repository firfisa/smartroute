#!/usr/bin/env ruby
# frozen_string_literal: true

require "cgi"
require "fileutils"
require "json"
require "open3"
require "optparse"

options = { label: "io.github.firfisa.smartroute" }
OptionParser.new do |parser|
  parser.on("--runtime PATH") { |value| options[:runtime] = value }
  parser.on("--output PATH") { |value| options[:output] = value }
  parser.on("--label LABEL") { |value| options[:label] = value }
end.parse!

abort "error: --runtime and --output are required" unless options.values_at(:runtime, :output).all?
abort "error: invalid launch agent label" unless options[:label].match?(/\A[A-Za-z0-9][A-Za-z0-9.-]{2,127}\z/)

runtime = File.realpath(options[:runtime])
output = File.expand_path(options[:output])
abort "error: runtime must be a private directory" unless File.directory?(runtime) && File.stat(runtime).mode & 0o077 == 0
abort "error: output already exists" if File.exist?(output)

manifest_path = File.join(runtime, "manifest.json")
runbook_path = File.join(runtime, "private-runbook.json")
config_path = File.realpath(File.join(runtime, "config.json"))
binary_path = File.realpath(File.join(runtime, "bin", "smartroute"))
abort "error: incomplete live runtime" unless File.file?(manifest_path) && File.file?(runbook_path)
abort "error: pinned SmartRoute is not executable" unless File.file?(binary_path) && File.executable?(binary_path)

manifest = JSON.parse(File.read(manifest_path))
runbook = JSON.parse(File.read(runbook_path))
abort "error: unsupported live runtime schema" unless manifest["schema_version"] == 1 && runbook["schema_version"] == 1

start_entries = runbook.fetch("commands").select { |entry| entry.is_a?(Hash) && entry["step"] == "start_supervisor" }
abort "error: live runtime must contain one supervisor command" unless start_entries.length == 1
arguments = start_entries.first["command"]
abort "error: invalid supervisor command" unless arguments.is_a?(Array) && arguments.all? { |value| value.is_a?(String) && !value.empty? }
abort "error: supervisor command does not use the pinned binary" unless File.realpath(arguments.fetch(0)) == binary_path
abort "error: supervisor command is not supervise" unless arguments.fetch(1) == "supervise"

config_indexes = arguments.each_index.select { |index| arguments[index] == "-config" }
abort "error: supervisor command must contain one config" unless config_indexes.length == 1
config_argument = arguments[config_indexes.first + 1]
abort "error: supervisor command config drifted" unless config_argument && File.realpath(config_argument) == config_path

session_indexes = arguments.each_index.select { |index| arguments[index] == "-trial-session" }
abort "error: supervisor command must contain one trial session" unless session_indexes.length == 1
session_argument = arguments[session_indexes.first + 1]
abort "error: supervisor command trial session drifted" unless session_argument == runbook["trial_session_id"] && session_argument&.match?(/\Atrial-[0-9a-f]{32}\z/)

_, _, validate_status = Open3.capture3(binary_path, "validate", "-config", config_path)
abort "error: pinned SmartRoute rejected the runtime config" unless validate_status.success?

logs_dir = File.join(runtime, "service-logs")
Dir.mkdir(logs_dir, 0o700) unless File.exist?(logs_dir)
abort "error: service log path is not a private directory" unless File.directory?(logs_dir) && File.stat(logs_dir).mode & 0o077 == 0

escape = ->(value) { CGI.escapeHTML(value.to_s) }
argument_xml = arguments.map { |argument| "      <string>#{escape.call(argument)}</string>" }.join("\n")
plist = <<~PLIST
  <?xml version="1.0" encoding="UTF-8"?>
  <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
  <plist version="1.0">
  <dict>
    <key>Label</key>
    <string>#{escape.call(options[:label])}</string>
    <key>ProgramArguments</key>
    <array>
  #{argument_xml}
    </array>
    <key>WorkingDirectory</key>
    <string>#{escape.call(runtime)}</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>ThrottleInterval</key>
    <integer>5</integer>
    <key>ExitTimeOut</key>
    <integer>10</integer>
    <key>Umask</key>
    <integer>63</integer>
    <key>StandardOutPath</key>
    <string>#{escape.call(File.join(logs_dir, "stdout.log"))}</string>
    <key>StandardErrorPath</key>
    <string>#{escape.call(File.join(logs_dir, "stderr.log"))}</string>
  </dict>
  </plist>
PLIST

FileUtils.mkdir_p(File.dirname(output), mode: 0o700)
File.open(output, File::WRONLY | File::CREAT | File::EXCL, 0o600) { |file| file.write(plist) }

_, lint_error, lint_status = Open3.capture3("plutil", "-lint", output)
unless lint_status.success?
  File.delete(output)
  abort "error: generated launch agent is invalid: #{lint_error.strip}"
end

puts JSON.generate({
  "prepared" => true,
  "label" => options[:label],
  "output" => output,
  "runtime" => runtime,
  "keep_alive" => true,
  "run_at_load" => true,
  "clash_files_read" => false,
  "clash_files_written" => false,
  "clash_reloaded" => false
})
