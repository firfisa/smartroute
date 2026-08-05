#!/usr/bin/env node

import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import vm from "node:vm";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { applySmartRoute, SMARTROUTE_NAMES } = require("../integrations/clash-verge/smartroute-transform.js");
const options = { enginePort: 17890, guardPort: 17893, directPort: 17891, proxyPort: 17892, originalPort: 17894 };

function fixtureOutputPath(args) {
  if (args.length === 0) return "";
  if (args.length !== 2 || args[0] !== "--write-fixture" || !args[1]) {
    throw new Error("usage: test-clash-transform.mjs [--write-fixture PATH]");
  }
  return path.resolve(args[1]);
}

const outputFixture = fixtureOutputPath(process.argv.slice(2));

function fixture() {
  return {
    mode: "rule",
    proxies: [
      { name: "NODE-A", type: "socks5", server: "127.0.0.1", port: 30001 },
      { name: "NODE-B", type: "socks5", server: "127.0.0.1", port: 30002 },
    ],
    "proxy-groups": [
      { name: "AUTO", type: "fallback", proxies: ["NODE-A", "NODE-B"] },
      { name: "PROXY-BRANCH", type: "select", proxies: ["AUTO", "NODE-A", "DIRECT-BRANCH"] },
      { name: "DIRECT-BRANCH", type: "select", proxies: ["DIRECT"] },
      { name: "ROOT", type: "select", proxies: ["PROXY-BRANCH", "DIRECT-BRANCH"] },
    ],
    rules: ["DOMAIN-SUFFIX,example.test,DIRECT", "MATCH,ROOT"],
  };
}

const transformed = applySmartRoute(fixture(), options);
assert.equal(transformed.rules[0], "DOMAIN-SUFFIX,example.test,DIRECT");
assert.equal(transformed.rules[1], `MATCH,${SMARTROUTE_NAMES.adapter}`);
assert.equal(transformed.proxies.length, 3);
assert.equal(transformed.listeners.length, 3);
assert.deepEqual(transformed.listeners.map(({ name, port, proxy }) => ({ name, port, proxy })), [
  { name: SMARTROUTE_NAMES.directListener, port: options.directPort, proxy: "DIRECT" },
  { name: SMARTROUTE_NAMES.proxyListener, port: options.proxyPort, proxy: "PROXY-BRANCH" },
  { name: SMARTROUTE_NAMES.originalListener, port: options.originalPort, proxy: "ROOT" },
]);
const once = JSON.stringify(transformed);
assert.equal(JSON.stringify(applySmartRoute(transformed, options)), once, "transform must be idempotent");

function rejects(mutator, pattern) {
  const value = fixture();
  mutator(value);
  assert.throws(() => applySmartRoute(value, options), pattern);
}
rejects((value) => value.rules.push("MATCH,ROOT"), /exactly one MATCH/);
rejects((value) => value.rules.push("DOMAIN,after.test,DIRECT"), /MATCH must be the final rule/);
rejects((value) => value["proxy-groups"].find((group) => group.name === "ROOT").proxies.unshift("AUTO"), /exactly two branches/);
rejects((value) => value["proxy-groups"].find((group) => group.name === "ROOT").proxies[1] = "UNKNOWN", /Direct-only branch/);
rejects((value) => value.proxies.push({ name: SMARTROUTE_NAMES.adapter }), /reserved adapter name/);
rejects((value) => value.listeners = [{ name: "existing", type: "mixed", port: options.directPort }], /listener port/);
rejects((value) => value.listeners = [{ name: "existing", type: "mixed", port: options.guardPort }], /listener port/);
rejects((value) => value["mixed-port"] = options.enginePort, /top-level listener port/);
rejects((value) => value.proxies.push({ name: "ROOT", type: "socks5" }), /names must not collide/);
assert.throws(() => applySmartRoute(fixture(), { ...options, proxyPort: options.directPort }), /ports must be distinct/);

const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "smartroute-compose-test-"));
try {
  const basePath = path.join(temporary, "base.js");
  const outputPath = path.join(temporary, "composed.js");
  fs.writeFileSync(basePath, "function main(config) { config.base_transform_preserved = true; return config; }\n", { mode: 0o600 });
  const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
  const composer = path.join(scriptDirectory, "compose-clash-script.mjs");
  const composedResult = spawnSync(process.execPath, [composer, "--base", basePath, "--output", outputPath], { encoding: "utf8" });
  assert.equal(composedResult.status, 0, composedResult.stderr);
  const context = vm.createContext({});
  vm.runInContext(fs.readFileSync(outputPath, "utf8"), context, { filename: "composed.js" });
  const composedConfig = context.main(fixture());
  assert.equal(composedConfig.base_transform_preserved, true);
  assert.equal(composedConfig.rules.at(-1), `MATCH,${SMARTROUTE_NAMES.adapter}`);
  const overwrite = spawnSync(process.execPath, [composer, "--base", basePath, "--output", outputPath], { encoding: "utf8" });
  assert.notEqual(overwrite.status, 0, "composer must refuse overwrite");
} finally {
  fs.rmSync(temporary, { recursive: true, force: true });
}

if (outputFixture) {
  const candidate = fixture();
  Object.assign(candidate, {
    "mixed-port": 0,
    "bind-address": "127.0.0.1",
    "allow-lan": false,
    "log-level": "silent",
    ipv6: false,
  });
  applySmartRoute(candidate, options);
  fs.mkdirSync(path.dirname(outputFixture), { recursive: true, mode: 0o700 });
  fs.writeFileSync(outputFixture, `${JSON.stringify(candidate, null, 2)}\n`, { mode: 0o600, flag: "wx" });
}

process.stdout.write("Clash Verge SmartRoute transform tests passed\n");
