#!/usr/bin/env ruby
# frozen_string_literal: true

require "fileutils"
require "json"
require "open3"
require "rexml/document"
require "tmpdir"

project_root = File.expand_path("..", __dir__)
preparer = File.join(project_root, "scripts", "prepare-macos-launch-agent.rb")

def write(path, content, mode = 0o600)
  File.write(path, content, mode: "wb")
  File.chmod(mode, path)
end

Dir.mktmpdir("smartroute-launch-agent-test-") do |temporary|
  runtime = File.join(temporary, "runtime")
  bin_dir = File.join(runtime, "bin")
  Dir.mkdir(runtime, 0o700)
  Dir.mkdir(bin_dir, 0o700)
  binary = File.join(bin_dir, "smartroute")
  write(binary, "#!/bin/sh\nexit 0\n", 0o700)
  config = File.join(runtime, "config.json")
  write(config, "{}\n")
  session = "trial-#{"a" * 32}"
  command = [binary, "supervise", "-acknowledge-direct-probes", "-config", config, "-network-profile", "synthetic-network", "-trial-session", session]
  write(File.join(runtime, "manifest.json"), JSON.generate({ "schema_version" => 1 }) + "\n")
  write(File.join(runtime, "private-runbook.json"), JSON.generate({
    "schema_version" => 1,
    "trial_session_id" => session,
    "commands" => [{ "step" => "start_supervisor", "command" => command }]
  }) + "\n")

  output = File.join(temporary, "agent", "io.github.firfisa.smartroute.test.plist")
  stdout, stderr, status = Open3.capture3(
    "ruby", preparer, "--runtime", runtime, "--output", output,
    "--label", "io.github.firfisa.smartroute.test"
  )
  raise "launch agent preparation failed: #{stderr}" unless status.success? && JSON.parse(stdout)["prepared"]
  raise "launch agent output is not private" unless File.stat(output).mode & 0o077 == 0
  raise "service log directory is not private" unless File.stat(File.join(runtime, "service-logs")).mode & 0o077 == 0

  document = REXML::Document.new(File.read(output))
  dict = document.elements["plist/dict"]
  values = {}
  children = dict.elements.to_a
  children.each_with_index do |element, index|
    next unless element.name == "key"

    values[element.text] = children[index + 1]
  end
  raise "wrong launch agent label" unless values.fetch("Label").text == "io.github.firfisa.smartroute.test"
  raise "RunAtLoad missing" unless values.fetch("RunAtLoad").name == "true"
  raise "KeepAlive missing" unless values.fetch("KeepAlive").name == "true"
  raise "unsafe umask" unless values.fetch("Umask").text == "63"
  rendered_arguments = values.fetch("ProgramArguments").elements.to_a.map(&:text)
  raise "supervisor arguments changed" unless rendered_arguments == command

  _, _, existing_status = Open3.capture3(
    "ruby", preparer, "--runtime", runtime, "--output", output,
    "--label", "io.github.firfisa.smartroute.test"
  )
  raise "existing launch agent was overwritten" if existing_status.success?

  drifted_runbook = JSON.parse(File.read(File.join(runtime, "private-runbook.json")))
  drifted_runbook["commands"][0]["command"][0] = "/bin/false"
  write(File.join(runtime, "private-runbook.json"), JSON.generate(drifted_runbook) + "\n")
  _, _, drift_status = Open3.capture3(
    "ruby", preparer, "--runtime", runtime,
    "--output", File.join(temporary, "drifted.plist")
  )
  raise "drifted pinned binary was accepted" if drift_status.success?
end

puts JSON.generate({
  "passed" => true,
  "synthetic" => true,
  "launchctl_called" => false,
  "active_clash_read" => false,
  "active_clash_write" => false
})
