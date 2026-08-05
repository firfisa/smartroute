#!/usr/bin/env node

import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

function fail(message) {
  process.stderr.write(`error: ${message}\n`);
  process.exit(1);
}

const args = process.argv.slice(2);
if (args.length !== 2 || args[0] !== "--mihomo") {
  fail("usage: test-clash-transform-mihomo.mjs --mihomo PATH");
}
const mihomo = path.resolve(args[1]);
if (!fs.statSync(mihomo, { throwIfNoEntry: false })?.isFile()) {
  fail("mihomo path must be an existing file");
}

const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "smartroute-transform-mihomo-"));
try {
  const configPath = path.join(temporary, "candidate.json");
  const homePath = path.join(temporary, "mihomo-home");
  fs.mkdirSync(homePath, { mode: 0o700 });
  const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
  const transformTest = spawnSync(process.execPath, [
    path.join(scriptDirectory, "test-clash-transform.mjs"),
    "--write-fixture", configPath,
  ], { encoding: "utf8" });
  if (transformTest.status !== 0) {
    throw new Error(transformTest.stderr || transformTest.stdout || "transform fixture generation failed");
  }
  const validation = spawnSync(mihomo, ["-t", "-d", homePath, "-f", configPath], { encoding: "utf8" });
  if (validation.status !== 0) {
    throw new Error(validation.stderr || validation.stdout || "mihomo rejected transformed fixture");
  }
  process.stdout.write(`${JSON.stringify({
    passed: true,
    synthetic: true,
    active_clash_read: false,
    active_clash_write: false,
    external_network: false,
    transform_tests: true,
    mihomo_config_validated: true,
  })}\n`);
} catch (error) {
  process.stderr.write(`error: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
} finally {
  fs.rmSync(temporary, { recursive: true, force: true });
}
