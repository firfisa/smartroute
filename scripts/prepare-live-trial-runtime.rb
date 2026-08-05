#!/usr/bin/env ruby
# frozen_string_literal: true

require "digest"
require "fileutils"
require "json"
require "open3"
require "optparse"
require "securerandom"
require "time"

options = { network_profile: "single-network-trial" }
OptionParser.new do |parser|
  parser.on("--package PATH") { |value| options[:package] = value }
  parser.on("--output PATH") { |value| options[:output] = value }
  parser.on("--smartroute PATH") { |value| options[:smartroute] = value }
  parser.on("--network-profile LABEL") { |value| options[:network_profile] = value }
end.parse!

abort "error: --package, --output, and --smartroute are required" unless options.values_at(:package, :output, :smartroute).all?
abort "error: network profile must not be empty" if options[:network_profile].empty?

project_root = File.expand_path("..", __dir__)
package = File.realpath(options[:package])
binary = File.realpath(options[:smartroute])
output = File.expand_path(options[:output])
abort "error: smartroute path must be a regular executable file" unless File.file?(binary) && File.executable?(binary)
abort "error: output already exists" if File.exist?(output)
abort "error: output must be outside the repository" if output == project_root || output.start_with?(project_root + File::SEPARATOR)
abort "error: output must be outside the candidate package" if output == package || output.start_with?(package + File::SEPARATOR)

def sha256(path)
  Digest::SHA256.file(path).hexdigest
end

def write_private(path, value, mode = 0o600)
  File.open(path, File::WRONLY | File::CREAT | File::EXCL, mode) { |file| file.write(value) }
end

manager = File.join(project_root, "scripts", "manage-active-clash-candidate.rb")
verify_out, _, verify_status = Open3.capture3("ruby", manager, "--package", package)
abort "error: candidate package verification failed" unless verify_status.success?
candidate_state = JSON.parse(verify_out)
abort "error: live runtime must be prepared while the active script is original" unless candidate_state["active_script_state"] == "original"

package_manifest_path = File.join(package, "manifest.json")
package_manifest = JSON.parse(File.read(package_manifest_path))
topology = package_manifest["runtime_topology"]
required_topology = %w[listen_address direct_endpoint proxy_endpoint guard_listen_address original_endpoint]
abort "error: candidate package lacks an exact runtime topology; regenerate it" unless topology.is_a?(Hash) && required_topology.all? { |name| topology[name].is_a?(String) }
abort "error: runtime topology addresses must be distinct" unless required_topology.map { |name| topology[name] }.uniq.length == required_topology.length

created = false
begin
  Dir.mkdir(output, 0o700)
  created = true
  incomplete = File.join(output, "INCOMPLETE")
  write_private(incomplete, "live runtime preparation has not completed\n")
  bin_dir = File.join(output, "bin")
  state_dir = File.join(output, "state")
  observations_dir = File.join(state_dir, "observations")
  Dir.mkdir(bin_dir, 0o700)
  Dir.mkdir(state_dir, 0o700)
  Dir.mkdir(observations_dir, 0o700)
  write_private(File.join(observations_dir, ".paused"), "")

  pinned_binary = File.join(bin_dir, "smartroute")
  FileUtils.cp(binary, pinned_binary)
  File.chmod(0o700, pinned_binary)

  config = {
    "version" => 1,
    "listen_address" => topology.fetch("listen_address"),
    "direct_endpoint" => topology.fetch("direct_endpoint"),
    "proxy_endpoint" => topology.fetch("proxy_endpoint"),
    "guard_listen_address" => topology.fetch("guard_listen_address"),
    "original_endpoint" => topology.fetch("original_endpoint"),
    "guard_adaptive_timeout_ms" => 250,
    "original_fallback" => "proxy",
    "decision" => {
      "direct_head_start_ms" => 200,
      "max_direct_penalty_ms" => 150,
      "candidate_timeout_ms" => 5000
    },
    "learning" => {
      "mode" => "auto",
      "max_entries" => 10_000,
      "persistence" => {
        "enabled" => true,
        "database_path" => File.join(state_dir, "learning.db"),
        "queue_size" => 256,
        "shutdown_timeout_ms" => 2000
      }
    },
    "fixed_policy" => { "database_path" => File.join(state_dir, "fixed-policies.db") },
    "privacy" => { "mode" => "explicit-opt-in", "never_direct_probe" => [] },
    "observation" => {
      "enabled" => true,
      "directory" => observations_dir,
      "max_file_bytes" => 1_048_576,
      "max_files_per_source" => 3,
      "retention_hours" => 24,
      "include_cleartext_hostname" => false
    }
  }
  config_path = File.join(output, "config.json")
  write_private(config_path, JSON.pretty_generate(config) + "\n")

  _, _, validate_status = Open3.capture3(pinned_binary, "validate", "-config", config_path)
  abort "error: pinned SmartRoute rejected the generated live config" unless validate_status.success?
  doctor_out, _, doctor_status = Open3.capture3(
    pinned_binary, "doctor", "-phase", "baseline", "-config", config_path
  )
  abort "error: live topology baseline is not free" unless doctor_status.success? && JSON.parse(doctor_out)["passed"]

  trial_session = "trial-#{SecureRandom.hex(16)}"
  commands = [
    { "step" => "baseline", "command" => [pinned_binary, "doctor", "-phase", "baseline", "-config", config_path] },
    { "step" => "resume_observations", "command" => [pinned_binary, "observations", "resume", "-config", config_path] },
    { "step" => "start_supervisor", "command" => [pinned_binary, "supervise", "-acknowledge-direct-probes", "-config", config_path, "-network-profile", options[:network_profile], "-trial-session", trial_session] },
    { "step" => "armed", "command" => [pinned_binary, "doctor", "-phase", "armed", "-config", config_path] },
    { "step" => "install_candidate", "command" => ["ruby", manager, "--package", package, "--action", "install", "--confirm-write"] },
    { "step" => "reload_clash", "command" => nil, "operator_action" => "reload Clash once using the separately verified active control path" },
    { "step" => "running", "command" => [pinned_binary, "doctor", "-phase", "running", "-config", config_path] },
    { "step" => "restore_original", "command" => ["ruby", manager, "--package", package, "--action", "rollback", "--confirm-write"] },
    { "step" => "reload_clash_rollback", "command" => nil, "operator_action" => "reload Clash once before stopping SmartRoute" },
    { "step" => "armed_after_rollback", "command" => [pinned_binary, "doctor", "-phase", "armed", "-config", config_path] },
    { "step" => "stop_supervisor", "command" => nil, "operator_action" => "send SIGTERM to the foreground supervisor and wait for it to drain" },
    { "step" => "pause_observations", "command" => [pinned_binary, "observations", "pause", "-config", config_path] },
    { "step" => "baseline_after_stop", "command" => [pinned_binary, "doctor", "-phase", "baseline", "-config", config_path] },
    { "step" => "report", "command" => [pinned_binary, "observations", "report", "-hours", "24", "-config", config_path] }
  ]
  runbook_path = File.join(output, "private-runbook.json")
  write_private(runbook_path, JSON.pretty_generate({
    "schema_version" => 1,
    "trial_session_id" => trial_session,
    "network_profile" => options[:network_profile],
    "must_remain_on_one_network" => true,
    "commands" => commands
  }) + "\n")
  manifest = {
    "schema_version" => 1,
    "created_at" => Time.now.utc.iso8601,
    "sensitive_local_workspace" => true,
    "candidate_package_manifest_sha256" => sha256(package_manifest_path),
    "smartroute_binary_sha256" => sha256(pinned_binary),
    "config_sha256" => sha256(config_path),
    "runtime_topology" => topology,
    "observation_bounds" => config.fetch("observation"),
    "validation" => {
      "candidate_state" => "original",
      "config_valid" => true,
      "baseline_ports_free" => true,
      "external_network" => false,
      "active_clash_write" => false,
      "clash_reload" => false
    },
    "sequence" => commands.map { |entry| entry.fetch("step") }
  }
  write_private(File.join(output, "manifest.json"), JSON.pretty_generate(manifest) + "\n")
  File.delete(incomplete)
  puts JSON.generate({
    "prepared" => true,
    "output" => output,
    "config_valid" => true,
    "baseline_ports_free" => true,
    "observation_paused" => true,
    "active_clash_write" => false,
    "clash_reloaded" => false
  })
rescue StandardError, SystemExit => error
  FileUtils.remove_entry_secure(output) if created && File.exist?(output)
  warn(error.is_a?(SystemExit) ? "error: live runtime preparation failed" : "error: #{error.message}")
  exit 1
end
