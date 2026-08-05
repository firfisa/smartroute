#!/usr/bin/env ruby
# frozen_string_literal: true

require "json"
require "open3"
require "socket"
require "tmpdir"

project_root = File.expand_path("..", __dir__)
relocator = File.join(project_root, "scripts", "relocate-live-runtime.rb")

def write(path, content, mode = 0o600)
  File.write(path, content, mode: "wb")
  File.chmod(mode, path)
end

Dir.mktmpdir("smartroute-relocate-test-") do |temporary|
  source = File.join(temporary, "source")
  output = File.join(temporary, "durable", "runtime")
  Dir.mkdir(source, 0o700)
  source = File.realpath(source)
  Dir.mkdir(File.join(source, "bin"), 0o700)
  Dir.mkdir(File.join(source, "state"), 0o700)
  Dir.mkdir(File.join(source, "service"), 0o700)
  binary = File.join(source, "bin", "smartroute")
  write(binary, "#!/bin/sh\nexit 0\n", 0o700)
  engine = TCPServer.new("127.0.0.1", 0)
  guard = TCPServer.new("127.0.0.1", 0)
  engine_port = engine.addr[1]
  guard_port = guard.addr[1]
  engine.close
  guard.close
  config = {
    "listen_address" => "127.0.0.1:#{engine_port}",
    "guard_listen_address" => "127.0.0.1:#{guard_port}",
    "learning" => { "persistence" => { "database_path" => File.join(source, "state", "learning.db") } },
    "fixed_policy" => { "database_path" => File.join(source, "state", "fixed.db") },
    "observation" => { "directory" => File.join(source, "state", "observations") }
  }
  write(File.join(source, "config.json"), JSON.generate(config) + "\n")
  write(File.join(source, "private-runbook.json"), JSON.generate({
    "schema_version" => 1,
    "commands" => [{ "step" => "start_supervisor", "command" => [binary, "supervise", "-config", File.join(source, "config.json")] }]
  }) + "\n")
  write(File.join(source, "manifest.json"), JSON.generate({ "schema_version" => 1 }) + "\n")
  write(File.join(source, "service", "old.plist"), "old\n")

  stdout, stderr, status = Open3.capture3("ruby", relocator, "--source", source, "--output", output)
  raise "runtime relocation failed: #{stderr}" unless status.success? && JSON.parse(stdout)["relocated"]
  relocated_config = JSON.parse(File.read(File.join(output, "config.json")))
  raise "database path was not rebased" unless relocated_config.dig("learning", "persistence", "database_path").start_with?(output)
  relocated_runbook = JSON.parse(File.read(File.join(output, "private-runbook.json")))
  raise "runbook was not rebased" unless relocated_runbook.dig("commands", 0, "command", 0).start_with?(output)
  raise "stale service plist was copied" unless Dir.glob(File.join(output, "service", "*.plist")).empty?
  raise "relocated runtime is not private" unless File.stat(output).mode & 0o077 == 0
end

puts JSON.generate({ passed: true, synthetic: true, network: "loopback-port-allocation-only", active_clash: false })
