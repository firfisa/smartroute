#!/usr/bin/env node

import fs from "node:fs";
import vm from "node:vm";

const args = process.argv.slice(2);
if (args.length !== 2 || args[0] !== "--script") {
  process.stderr.write("error: usage: apply-composed-clash-script.mjs --script PATH\n");
  process.exit(1);
}

const chunks = [];
let bytes = 0;
for await (const chunk of process.stdin) {
  bytes += chunk.length;
  if (bytes > 32 * 1024 * 1024) {
    throw new Error("input configuration exceeds 32 MiB");
  }
  chunks.push(chunk);
}

const input = JSON.parse(Buffer.concat(chunks).toString("utf8"));
const context = vm.createContext({ __smartrouteInput: input });
const source = fs.readFileSync(args[1], "utf8");
vm.runInContext(source, context, { filename: "composed-clash-script.js", timeout: 2000 });
const encoded = vm.runInContext("JSON.stringify(main(__smartrouteInput))", context, { timeout: 2000 });
if (typeof encoded !== "string") {
  throw new Error("composed main(config) did not return a serializable object");
}
process.stdout.write(`${encoded}\n`);
