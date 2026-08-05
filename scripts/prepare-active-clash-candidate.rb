#!/usr/bin/env ruby
# frozen_string_literal: true

require "date"
require "digest"
require "fileutils"
require "json"
require "open3"
require "optparse"
require "tmpdir"
require "time"
require "yaml"

options = {
  engine_port: 17_890,
  direct_port: 17_891,
  proxy_port: 17_892,
  guard_port: 17_893,
  original_port: 17_894
}
OptionParser.new do |parser|
  parser.on("--app-dir PATH") { |value| options[:app_dir] = value }
  parser.on("--output PATH") { |value| options[:output] = value }
  parser.on("--mihomo PATH") { |value| options[:mihomo] = value }
  parser.on("--engine-port PORT", Integer) { |value| options[:engine_port] = value }
  parser.on("--direct-port PORT", Integer) { |value| options[:direct_port] = value }
  parser.on("--proxy-port PORT", Integer) { |value| options[:proxy_port] = value }
  parser.on("--guard-port PORT", Integer) { |value| options[:guard_port] = value }
  parser.on("--original-port PORT", Integer) { |value| options[:original_port] = value }
end.parse!

abort "error: --app-dir, --output, and --mihomo are required" unless options.values_at(:app_dir, :output, :mihomo).all?

project_root = File.expand_path("..", __dir__)
app_dir = File.realpath(options[:app_dir])
mihomo = File.realpath(options[:mihomo])
output = File.expand_path(options[:output])
abort "error: output already exists" if File.exist?(output)
abort "error: output must be outside the active Clash directory" if output == app_dir || output.start_with?(app_dir + File::SEPARATOR)
abort "error: output must be outside the repository" if output == project_root || output.start_with?(project_root + File::SEPARATOR)
abort "error: mihomo path must be a regular file" unless File.file?(mihomo)

ports = options.values_at(:engine_port, :direct_port, :proxy_port, :guard_port, :original_port)
abort "error: runtime ports must be distinct integers in 1..65535" unless ports.uniq.length == 5 && ports.all? { |port| port.is_a?(Integer) && port.between?(1, 65_535) }

topology = {
  "listen_address" => "127.0.0.1:#{options[:engine_port]}",
  "direct_endpoint" => "127.0.0.1:#{options[:direct_port]}",
  "proxy_endpoint" => "127.0.0.1:#{options[:proxy_port]}",
  "guard_listen_address" => "127.0.0.1:#{options[:guard_port]}",
  "original_endpoint" => "127.0.0.1:#{options[:original_port]}"
}.freeze

def load_yaml(path)
  YAML.safe_load(File.read(path), permitted_classes: [Time, Date, Symbol], aliases: true)
end

def sha256(path)
  Digest::SHA256.file(path).hexdigest
end

def write_private(path, value)
  File.open(path, File::WRONLY | File::CREAT | File::EXCL, 0o600) do |file|
    file.write(value)
  end
end

created = false
begin
  profiles_path = File.join(app_dir, "profiles.yaml")
  generated_path = File.join(app_dir, "clash-verge.yaml")
  abort "error: active profiles or generated config is missing" unless File.file?(profiles_path) && File.file?(generated_path)

  profiles = load_yaml(profiles_path)
  items = profiles.fetch("items")
  current = items.find { |item| item["uid"] == profiles["current"] }
  abort "error: current profile is missing" unless current
  script_ref = current.dig("option", "script")
  script_item = items.find { |item| item["uid"] == script_ref }
  abort "error: active script binding is missing" unless script_item && script_item["file"].is_a?(String)

  profiles_dir = File.realpath(File.join(app_dir, "profiles"))
  active_script = File.realpath(File.join(profiles_dir, script_item["file"]))
  abort "error: active script escaped the profiles directory" unless active_script.start_with?(profiles_dir + File::SEPARATOR)
  abort "error: active script is not a regular file" unless File.file?(active_script)

  Dir.mkdir(output, 0o700)
  created = true
  backup_dir = File.join(output, "backup")
  candidate_dir = File.join(output, "candidate")
  Dir.mkdir(backup_dir, 0o700)
  Dir.mkdir(candidate_dir, 0o700)
  incomplete = File.join(output, "INCOMPLETE")
  write_private(incomplete, "candidate preparation has not completed\n")

  backup_script = File.join(backup_dir, "original-script.js")
  File.open(backup_script, File::WRONLY | File::CREAT | File::EXCL, 0o600) do |target|
    File.open(active_script, "rb") { |source| IO.copy_stream(source, target) }
  end
  candidate_script = File.join(candidate_dir, "composed-script.js")
  _, _, compose_status = Open3.capture3(
    "node", File.join(project_root, "scripts", "compose-clash-script.mjs"),
    "--base", active_script, "--output", candidate_script,
    "--engine-port", topology.fetch("listen_address").split(":").last,
    "--direct-port", topology.fetch("direct_endpoint").split(":").last,
    "--proxy-port", topology.fetch("proxy_endpoint").split(":").last,
    "--guard-port", topology.fetch("guard_listen_address").split(":").last,
    "--original-port", topology.fetch("original_endpoint").split(":").last
  )
  abort "error: active script composition failed" unless compose_status.success?
  _, _, syntax_status = Open3.capture3("node", "--check", candidate_script)
  abort "error: composed script syntax validation failed" unless syntax_status.success?

  original = load_yaml(generated_path)
  transformed_json, _, transform_status = Open3.capture3(
    "node", File.join(project_root, "scripts", "apply-composed-clash-script.mjs"),
    "--script", candidate_script,
    stdin_data: JSON.generate(original)
  )
  abort "error: composed script rejected the current generated config" unless transform_status.success?
  transformed = JSON.parse(transformed_json)

  original_rules = original.fetch("rules")
  transformed_rules = transformed.fetch("rules")
  original_proxies = original.fetch("proxies")
  transformed_proxies = transformed.fetch("proxies")
  original_groups = original.fetch("proxy-groups")
  transformed_groups = transformed.fetch("proxy-groups")
  original_listeners = original.fetch("listeners", [])
  transformed_listeners = transformed.fetch("listeners")
  semantic_checks = {
    "rule_count_unchanged" => transformed_rules.length == original_rules.length,
    "high_confidence_rules_unchanged" => transformed_rules[0...-1] == original_rules[0...-1],
    "final_match_replaced" => transformed_rules.last == "MATCH,SMARTROUTE-GUARD-ADAPTER",
    "one_proxy_added" => transformed_proxies.length == original_proxies.length + 1 && transformed_proxies[0, original_proxies.length] == original_proxies,
    "groups_unchanged" => transformed_groups == original_groups,
    "three_listeners_added" => transformed_listeners.length == original_listeners.length + 3 && transformed_listeners[0, original_listeners.length] == original_listeners
  }
  abort "error: transformed config failed semantic checks" unless semantic_checks.values.all?

  mihomo_valid = false
  validation_geodata = []
  Dir.mktmpdir("smartroute-active-candidate-") do |temporary|
    config_path = File.join(temporary, "candidate.json")
    home_path = File.join(temporary, "mihomo-home")
    Dir.mkdir(home_path, 0o700)
    %w[Country.mmdb GeoIP.dat GeoSite.dat ASN.mmdb geoip.metadb].each do |name|
      source = File.join(app_dir, name)
      next unless File.file?(source)

      FileUtils.cp(source, File.join(home_path, name))
      validation_geodata << name
    end
    write_private(config_path, JSON.generate(transformed))
    _, _, validation_status = Open3.capture3(mihomo, "-t", "-d", home_path, "-f", config_path)
    mihomo_valid = validation_status.success?
  end
  abort "error: pinned Mihomo rejected current transformed config" unless mihomo_valid

  manifest = {
    "schema_version" => 1,
    "created_at" => Time.now.utc.iso8601,
    "sensitive_local_package" => true,
    "active_state" => {
      "profiles_sha256" => sha256(profiles_path),
      "generated_config_sha256" => sha256(generated_path),
      "active_script_sha256" => sha256(active_script)
    },
    "candidate" => {
      "composed_script_sha256" => sha256(candidate_script),
      "backup_script_sha256" => sha256(backup_script)
    },
    "runtime_topology" => topology,
    "redacted_semantic_diff" => {
      "rules_before" => original_rules.length,
      "rules_after" => transformed_rules.length,
      "proxies_added" => transformed_proxies.length - original_proxies.length,
      "groups_changed" => 0,
      "listeners_added" => transformed_listeners.length - original_listeners.length,
      "checks" => semantic_checks
    },
    "validation" => {
      "composed_script_syntax" => true,
      "current_generated_config_transformed" => true,
      "pinned_mihomo_config_validated" => true,
      "temporary_geodata_files" => validation_geodata.sort
    },
    "safety" => {
      "active_directory_written" => false,
      "clash_reloaded" => false,
      "system_proxy_changed" => false,
      "tun_changed" => false
    }
  }
  write_private(File.join(output, "manifest.json"), JSON.pretty_generate(manifest) + "\n")
  rollback = {
    "profiles_path" => profiles_path,
    "active_script_path" => active_script,
    "expected_profiles_sha256" => manifest.dig("active_state", "profiles_sha256"),
    "expected_active_script_sha256" => manifest.dig("active_state", "active_script_sha256"),
    "active_script_mode" => format("%o", File.stat(active_script).mode & 0o777),
    "backup_relative_path" => "backup/original-script.js",
    "candidate_relative_path" => "candidate/composed-script.js"
  }
  write_private(File.join(output, "private-rollback.json"), JSON.pretty_generate(rollback) + "\n")
  File.delete(incomplete)
  puts JSON.generate({
    "prepared" => true,
    "output" => output,
    "rules_unchanged" => semantic_checks["high_confidence_rules_unchanged"],
    "mihomo_validated" => mihomo_valid,
    "active_directory_written" => false,
    "clash_reloaded" => false
  })
rescue StandardError, SystemExit => error
  FileUtils.remove_entry_secure(output) if created && File.exist?(output)
  warn(error.is_a?(SystemExit) ? "error: candidate preparation failed" : "error: #{error.message}")
  exit 1
end
