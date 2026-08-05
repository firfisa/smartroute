#!/usr/bin/env ruby
# frozen_string_literal: true

require "digest"
require "fileutils"
require "json"
require "open3"
require "optparse"
require "socket"
require "tmpdir"
require "yaml"

options = {}
OptionParser.new do |parser|
  parser.on("--mihomo PATH") { |value| options[:mihomo] = value }
  parser.on("--smartroute PATH") { |value| options[:smartroute] = value }
end.parse!
abort "error: --mihomo and --smartroute are required" unless options.values_at(:mihomo, :smartroute).all?

project_root = File.expand_path("..", __dir__)
generator = File.join(project_root, "scripts", "prepare-active-clash-candidate.rb")
manager = File.join(project_root, "scripts", "manage-active-clash-candidate.rb")
runtime_preparer = File.join(project_root, "scripts", "prepare-live-trial-runtime.rb")

def write(path, content)
  File.write(path, content, mode: "wb")
  File.chmod(0o600, path)
end

Dir.mktmpdir("smartroute-active-candidate-test-") do |temporary|
  app = File.join(temporary, "app")
  profiles_dir = File.join(app, "profiles")
  output = File.join(temporary, "candidate-package")
  Dir.mkdir(app, 0o700)
  Dir.mkdir(profiles_dir, 0o700)
  script_path = File.join(profiles_dir, "active.js")
  write(script_path, "function main(config) { config.base_script_preserved = true; return config; }\n")
  profiles = {
    "current" => "base",
    "items" => [
      { "uid" => "base", "type" => "remote", "file" => "base.yaml", "option" => { "script" => "script" } },
      { "uid" => "script", "type" => "script", "file" => "active.js" }
    ]
  }
  write(File.join(app, "profiles.yaml"), YAML.dump(profiles))
  config = {
    "mixed-port" => 0,
    "bind-address" => "127.0.0.1",
    "allow-lan" => false,
    "mode" => "rule",
    "log-level" => "silent",
    "ipv6" => false,
    "proxies" => [
      { "name" => "NODE-A", "type" => "socks5", "server" => "127.0.0.1", "port" => 30_001 },
      { "name" => "NODE-B", "type" => "socks5", "server" => "127.0.0.1", "port" => 30_002 }
    ],
    "proxy-groups" => [
      { "name" => "AUTO", "type" => "fallback", "proxies" => ["NODE-A", "NODE-B"] },
      { "name" => "PROXY-BRANCH", "type" => "select", "proxies" => ["AUTO", "NODE-A", "DIRECT-BRANCH"] },
      { "name" => "DIRECT-BRANCH", "type" => "select", "proxies" => ["DIRECT"] },
      { "name" => "ROOT", "type" => "select", "proxies" => ["PROXY-BRANCH", "DIRECT-BRANCH"] }
    ],
    "rules" => ["DOMAIN-SUFFIX,example.test,DIRECT", "MATCH,ROOT"]
  }
  generated_path = File.join(app, "clash-verge.yaml")
  write(generated_path, YAML.dump(config))
  before = [Digest::SHA256.file(script_path).hexdigest, Digest::SHA256.file(generated_path).hexdigest]
  port_sockets = Array.new(5) { TCPServer.new("127.0.0.1", 0) }
  ports = port_sockets.map { |socket| socket.addr[1] }
  port_sockets.each(&:close)
  port_args = [
    "--engine-port", ports[0].to_s,
    "--direct-port", ports[1].to_s,
    "--proxy-port", ports[2].to_s,
    "--guard-port", ports[3].to_s,
    "--original-port", ports[4].to_s
  ]

  stdout, stderr, status = Open3.capture3(
    "ruby", generator, "--app-dir", app, "--output", output, "--mihomo", options[:mihomo], *port_args
  )
  abort "candidate test failed: #{stderr}" unless status.success?
  result = JSON.parse(stdout)
  manifest = JSON.parse(File.read(File.join(output, "manifest.json")))
  rollback = JSON.parse(File.read(File.join(output, "private-rollback.json")))
  after = [Digest::SHA256.file(script_path).hexdigest, Digest::SHA256.file(generated_path).hexdigest]

  raise "generator did not report success" unless result["prepared"] && result["mihomo_validated"]
  raise "source files changed" unless before == after
  raise "semantic checks incomplete" unless manifest.dig("redacted_semantic_diff", "checks")&.values&.all?
  raise "runtime topology missing" unless manifest.dig("runtime_topology", "guard_listen_address") == "127.0.0.1:#{ports[3]}"
  raise "unsafe package claim" unless manifest.dig("safety", "active_directory_written") == false
  raise "rollback target mismatch" unless rollback["active_script_path"] == File.realpath(script_path)
  raise "incomplete marker remains" if File.exist?(File.join(output, "INCOMPLETE"))
  raise "package directory is not private" unless File.stat(output).mode & 0o077 == 0
  Dir.glob(File.join(output, "**", "*"), File::FNM_DOTMATCH).each do |path|
    next if [".", ".."].include?(File.basename(path)) || File.directory?(path)

    raise "package file is not private" unless File.stat(path).mode & 0o077 == 0
  end

  verify_out, verify_err, verify_status = Open3.capture3("ruby", manager, "--package", output)
  raise "package verification failed: #{verify_err}" unless verify_status.success? && JSON.parse(verify_out)["active_script_state"] == "original"

  metadata_profiles = Marshal.load(Marshal.dump(profiles))
  metadata_profiles["updated_at"] = "synthetic-metadata-only-change"
  write(File.join(app, "profiles.yaml"), YAML.dump(metadata_profiles))
  metadata_out, metadata_err, metadata_status = Open3.capture3("ruby", manager, "--package", output)
  raise "metadata-only profile change was rejected: #{metadata_err}" unless metadata_status.success? && JSON.parse(metadata_out)["active_script_state"] == "original"

  alternate_script = File.join(profiles_dir, "alternate.js")
  write(alternate_script, "function main(config) { config.alternate = true; return config; }\n")
  drifted_profiles = Marshal.load(Marshal.dump(metadata_profiles))
  drifted_profiles["items"] << { "uid" => "alternate-script", "type" => "script", "file" => "alternate.js" }
  drifted_profiles["items"].find { |item| item["uid"] == "base" }["option"]["script"] = "alternate-script"
  write(File.join(app, "profiles.yaml"), YAML.dump(drifted_profiles))
  _, _, binding_drift_status = Open3.capture3("ruby", manager, "--package", output)
  raise "actual script binding drift was accepted" if binding_drift_status.success?
  write(File.join(app, "profiles.yaml"), YAML.dump(metadata_profiles))

  runtime_output = File.join(temporary, "live-runtime")
  runtime_out, runtime_err, runtime_status = Open3.capture3(
    "ruby", runtime_preparer, "--package", output, "--output", runtime_output,
    "--smartroute", options[:smartroute], "--network-profile", "synthetic-network"
  )
  raise "runtime preparation failed: #{runtime_err}" unless runtime_status.success? && JSON.parse(runtime_out)["prepared"]
  runtime_manifest = JSON.parse(File.read(File.join(runtime_output, "manifest.json")))
  runtime_runbook = JSON.parse(File.read(File.join(runtime_output, "private-runbook.json")))
  expected_sequence = %w[baseline resume_observations start_supervisor armed install_candidate reload_clash running restore_original reload_clash_rollback armed_after_rollback stop_supervisor pause_observations baseline_after_stop report]
  raise "unsafe runtime sequence" unless runtime_manifest["sequence"] == expected_sequence
  raise "runtime config was not paused" unless File.file?(File.join(runtime_output, "state", "observations", ".paused"))
  raise "runtime package leaked permissions" unless File.stat(runtime_output).mode & 0o077 == 0
  raise "runtime session is not random" unless runtime_runbook["trial_session_id"]&.match?(/\Atrial-[0-9a-f]{32}\z/)
  _, validate_err, validate_status = Open3.capture3(
    File.join(runtime_output, "bin", "smartroute"), "validate", "-config", File.join(runtime_output, "config.json")
  )
  raise "prepared runtime config is invalid: #{validate_err}" unless validate_status.success?
  _, _, unconfirmed_status = Open3.capture3("ruby", manager, "--package", output, "--action", "install")
  raise "unconfirmed install was accepted" if unconfirmed_status.success?
  install_out, install_err, install_status = Open3.capture3(
    "ruby", manager, "--package", output, "--action", "install", "--confirm-write"
  )
  raise "synthetic install failed: #{install_err}" unless install_status.success? && JSON.parse(install_out)["active_script_state"] == "candidate"
  rollback_out, rollback_err, rollback_status = Open3.capture3(
    "ruby", manager, "--package", output, "--action", "rollback", "--confirm-write"
  )
  raise "synthetic rollback failed: #{rollback_err}" unless rollback_status.success? && JSON.parse(rollback_out)["active_script_state"] == "original"
  raise "rollback did not restore exact script" unless Digest::SHA256.file(script_path).hexdigest == before[0]

  _, _, repeat_status = Open3.capture3(
    "ruby", generator, "--app-dir", app, "--output", output, "--mihomo", options[:mihomo], *port_args
  )
  raise "existing output was overwritten" if repeat_status.success?

  bad_output = File.join(temporary, "bad-package")
  config["proxy-groups"].find { |group| group["name"] == "ROOT" }["proxies"] << "AUTO"
  write(generated_path, YAML.dump(config))
  _, _, bad_status = Open3.capture3(
    "ruby", generator, "--app-dir", app, "--output", bad_output, "--mihomo", options[:mihomo], *port_args
  )
  raise "ambiguous config was accepted" if bad_status.success?
  raise "failed candidate left sensitive output" if File.exist?(bad_output)
end

puts JSON.generate({ passed: true, synthetic: true, active_clash_read: false, active_clash_write: false })
