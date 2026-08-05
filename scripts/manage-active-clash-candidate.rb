#!/usr/bin/env ruby
# frozen_string_literal: true

require "digest"
require "date"
require "json"
require "optparse"
require "tempfile"
require "time"
require "yaml"

options = { action: "verify", confirm: false }
OptionParser.new do |parser|
  parser.on("--package PATH") { |value| options[:package] = value }
  parser.on("--action ACTION") { |value| options[:action] = value }
  parser.on("--confirm-write") { options[:confirm] = true }
end.parse!

abort "error: --package is required" unless options[:package]
abort "error: action must be verify, install, or rollback" unless %w[verify install rollback].include?(options[:action])
abort "error: install and rollback require --confirm-write" if options[:action] != "verify" && !options[:confirm]

package = File.realpath(options[:package])
abort "error: incomplete package" if File.exist?(File.join(package, "INCOMPLETE"))
manifest = JSON.parse(File.read(File.join(package, "manifest.json")))
rollback = JSON.parse(File.read(File.join(package, "private-rollback.json")))
abort "error: unsupported package schema" unless manifest["schema_version"] == 1

def sha256(path)
  Digest::SHA256.file(path).hexdigest
end

def package_path(root, relative)
  path = File.realpath(File.join(root, relative))
  abort "error: package path escaped package root" unless path.start_with?(root + File::SEPARATOR)

  path
end

def atomic_replace(target, source, mode)
  directory = File.dirname(target)
  temporary = Tempfile.new([".smartroute-", ".tmp"], directory)
  begin
    temporary.binmode
    File.open(source, "rb") { |input| IO.copy_stream(input, temporary) }
    temporary.flush
    temporary.fsync
    File.chmod(mode, temporary.path)
    File.rename(temporary.path, target)
  ensure
    temporary.close!
  end
end

def resolve_active_script(profiles_path)
  profiles = YAML.safe_load(
    File.read(profiles_path),
    permitted_classes: [Time, Date, Symbol],
    aliases: true
  )
  abort "error: active profile binding is invalid" unless profiles.is_a?(Hash) && profiles["items"].is_a?(Array)

  items = profiles.fetch("items")
  current_items = items.select { |item| item.is_a?(Hash) && item["uid"] == profiles["current"] }
  abort "error: active profile binding is invalid" unless current_items.length == 1

  script_ref = current_items.first.dig("option", "script")
  script_items = items.select { |item| item.is_a?(Hash) && item["uid"] == script_ref }
  abort "error: active profile binding is invalid" unless script_items.length == 1

  script_file = script_items.first["file"]
  abort "error: active profile binding is invalid" unless script_file.is_a?(String) && !script_file.empty?

  profiles_dir = File.realpath(File.join(File.dirname(profiles_path), "profiles"))
  resolved = File.realpath(File.join(profiles_dir, script_file))
  abort "error: active script escaped the profiles directory" unless resolved.start_with?(profiles_dir + File::SEPARATOR)
  abort "error: active script is not a regular file" unless File.file?(resolved)

  resolved
rescue Psych::Exception, KeyError, TypeError, Errno::ENOENT, Errno::EACCES
  abort "error: active profile binding is invalid"
end

profiles_path = rollback.fetch("profiles_path")
active_script = rollback.fetch("active_script_path")
candidate = package_path(package, rollback.fetch("candidate_relative_path"))
backup = package_path(package, rollback.fetch("backup_relative_path"))
mode = Integer(rollback.fetch("active_script_mode"), 8)
abort "error: active script mode is unsafe" unless mode.positive? && mode & 0o022 == 0
abort "error: active files are missing" unless File.file?(profiles_path) && File.file?(active_script)

expected_original = rollback.fetch("expected_active_script_sha256")
expected_candidate = manifest.dig("candidate", "composed_script_sha256")
expected_backup = manifest.dig("candidate", "backup_script_sha256")
abort "error: package backup checksum mismatch" unless sha256(backup) == expected_backup && expected_backup == expected_original
abort "error: package candidate checksum mismatch" unless sha256(candidate) == expected_candidate
abort "error: active profile binding drifted" unless resolve_active_script(profiles_path) == File.realpath(active_script)

current_hash = sha256(active_script)
state = if current_hash == expected_original
          "original"
        elsif current_hash == expected_candidate
          "candidate"
        else
          "drifted"
        end

case options[:action]
when "install"
  abort "error: active script is not the verified original" unless state == "original"
  atomic_replace(active_script, candidate, mode)
  abort "error: installed script checksum mismatch" unless sha256(active_script) == expected_candidate
  state = "candidate"
when "rollback"
  abort "error: active script is not the verified candidate" unless state == "candidate"
  atomic_replace(active_script, backup, mode)
  abort "error: restored script checksum mismatch" unless sha256(active_script) == expected_original
  state = "original"
end

puts JSON.generate({
  "verified" => true,
  "action" => options[:action],
  "active_script_state" => state,
  "clash_reloaded" => false,
  "system_proxy_changed" => false,
  "tun_changed" => false
})
