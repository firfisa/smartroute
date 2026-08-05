#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

function fail(message) {
  process.stderr.write(`error: ${message}\n`);
  process.exit(1);
}

function argumentsMap(values) {
  const result = new Map();
  for (let index = 0; index < values.length; index += 2) {
    const name = values[index];
    const value = values[index + 1];
    if (!name || !name.startsWith("--") || value === undefined || value.startsWith("--")) {
      fail("arguments must be --name value pairs");
    }
    if (result.has(name)) fail(`duplicate argument ${name}`);
    result.set(name, value);
  }
  return result;
}

const args = argumentsMap(process.argv.slice(2));
const basePath = args.get("--base");
const outputPath = args.get("--output");
if (!basePath || !outputPath) fail("--base and --output are required");

const parsePort = (name, fallback) => {
  const raw = args.get(name) ?? String(fallback);
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 1 || value > 65535) fail(`${name} must be an integer in 1..65535`);
  return value;
};
const options = {
  enginePort: parsePort("--engine-port", 17890),
  guardPort: parsePort("--guard-port", 17893),
  directPort: parsePort("--direct-port", 17891),
  proxyPort: parsePort("--proxy-port", 17892),
  originalPort: parsePort("--original-port", 17894),
};
if (new Set(Object.values(options)).size !== 5) fail("all five ports must be distinct");

const base = fs.readFileSync(basePath, "utf8");
if (!/\bfunction\s+main\s*\(/.test(base)) fail("base script must define function main(config)");
if (base.includes("__smartrouteBaseMain") || base.includes("__smartrouteApply") || base.includes("applySmartRoute")) {
  fail("base script already contains SmartRoute composition markers");
}

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const transformPath = path.resolve(scriptDirectory, "../integrations/clash-verge/smartroute-transform.js");
const transform = fs.readFileSync(transformPath, "utf8");
const composed = `${base.trimEnd()}\n\nconst __smartrouteBaseMain = main;\nconst __smartrouteApply = (() => {\n${transform.trim()}\n  return applySmartRoute;\n})();\nconst __smartrouteOptions = Object.freeze(${JSON.stringify(options)});\nmain = function smartrouteComposedMain(config) {\n  const baseResult = __smartrouteBaseMain(config);\n  if (!baseResult || typeof baseResult !== "object") {\n    throw new Error("SmartRoute transform: existing main(config) did not return a config object");\n  }\n  return __smartrouteApply(baseResult, __smartrouteOptions);\n};\n`;

fs.mkdirSync(path.dirname(path.resolve(outputPath)), { recursive: true, mode: 0o700 });
const descriptor = fs.openSync(outputPath, "wx", 0o600);
try {
  fs.writeFileSync(descriptor, composed, "utf8");
} finally {
  fs.closeSync(descriptor);
}
process.stdout.write(`${JSON.stringify({ written: true, output: path.resolve(outputPath), options })}\n`);
