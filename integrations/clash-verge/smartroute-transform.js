"use strict";

const SMARTROUTE_NAMES = Object.freeze({
  adapter: "SMARTROUTE-GUARD-ADAPTER",
  directListener: "smartroute-direct",
  proxyListener: "smartroute-proxy",
  originalListener: "smartroute-original",
});

function smartrouteAssert(condition, message) {
  if (!condition) {
    throw new Error(`SmartRoute transform: ${message}`);
  }
}

function smartrouteParseRule(rule) {
  smartrouteAssert(typeof rule === "string", "every rule must be a string");
  return rule.split(",").map((part) => part.trim());
}

function smartrouteValidateOptions(options) {
  smartrouteAssert(options && typeof options === "object", "options are required");
  const ports = [options.enginePort, options.guardPort, options.directPort, options.proxyPort, options.originalPort];
  for (const port of ports) {
    smartrouteAssert(Number.isInteger(port) && port >= 1 && port <= 65535, "all ports must be integers in 1..65535");
  }
  smartrouteAssert(new Set(ports).size === ports.length, "Guard, Direct, Proxy, and Original ports must be distinct");
}

function smartrouteAssertPortIsolation(config, options, allowedListenerNames = new Set()) {
  const reservedPorts = new Set([
    options.enginePort,
    options.guardPort,
    options.directPort,
    options.proxyPort,
    options.originalPort,
  ]);
  for (const key of ["port", "socks-port", "mixed-port", "redir-port", "tproxy-port"]) {
    if (Number.isInteger(config[key]) && config[key] !== 0 && reservedPorts.has(config[key])) {
      smartrouteAssert(false, `top-level listener port ${config[key]} collides with SmartRoute`);
    }
  }
  for (const listener of config.listeners) {
    if (listener && reservedPorts.has(listener.port) && !allowedListenerNames.has(listener.name)) {
      smartrouteAssert(false, `listener port ${listener.port} collides with SmartRoute`);
    }
  }
}

function smartrouteIndexes(config) {
  const groups = new Map();
  const proxies = new Set();
  for (const group of config["proxy-groups"]) {
    smartrouteAssert(group && typeof group === "object" && typeof group.name === "string", "every proxy group must have a name");
    smartrouteAssert(!groups.has(group.name), "proxy-group names must be unique");
    groups.set(group.name, group);
  }
  for (const proxy of config.proxies) {
    smartrouteAssert(proxy && typeof proxy === "object" && typeof proxy.name === "string", "every proxy must have a name");
    smartrouteAssert(!proxies.has(proxy.name), "proxy names must be unique");
    proxies.add(proxy.name);
  }
  for (const name of groups.keys()) {
    smartrouteAssert(!proxies.has(name), "proxy and proxy-group names must not collide");
  }
  return { groups, proxies };
}

function smartrouteTerminalStats(name, indexes, visiting) {
  if (name === "DIRECT") return { direct: 1, proxy: 0, reject: 0, unknown: 0 };
  if (name === "REJECT" || name === "REJECT-DROP") return { direct: 0, proxy: 0, reject: 1, unknown: 0 };
  if (indexes.proxies.has(name)) return { direct: 0, proxy: 1, reject: 0, unknown: 0 };
  const group = indexes.groups.get(name);
  if (!group) return { direct: 0, proxy: 0, reject: 0, unknown: 1 };
  smartrouteAssert(!visiting.has(name), "proxy-group graph contains a cycle");
  smartrouteAssert(Array.isArray(group.proxies) && group.proxies.length > 0, "every traversed proxy group must have members");
  const next = new Set(visiting);
  next.add(name);
  const total = { direct: 0, proxy: 0, reject: 0, unknown: 0 };
  for (const member of group.proxies) {
    smartrouteAssert(typeof member === "string", "proxy-group members must be names");
    const child = smartrouteTerminalStats(member, indexes, next);
    total.direct += child.direct;
    total.proxy += child.proxy;
    total.reject += child.reject;
    total.unknown += child.unknown;
  }
  return total;
}

function smartrouteListener(config, name) {
  return config.listeners.find((listener) => listener && listener.name === name);
}

function smartrouteVerifyExisting(config, options, indexes, matchParts) {
  const adapter = config.proxies.find((proxy) => proxy && proxy.name === SMARTROUTE_NAMES.adapter);
  const direct = smartrouteListener(config, SMARTROUTE_NAMES.directListener);
  const proxy = smartrouteListener(config, SMARTROUTE_NAMES.proxyListener);
  const original = smartrouteListener(config, SMARTROUTE_NAMES.originalListener);
  smartrouteAssert(adapter && direct && proxy && original, "partial existing SmartRoute objects are not accepted");
  smartrouteAssertPortIsolation(config, options, new Set([
    SMARTROUTE_NAMES.directListener,
    SMARTROUTE_NAMES.proxyListener,
    SMARTROUTE_NAMES.originalListener,
  ]));
  smartrouteAssert(matchParts[1] === SMARTROUTE_NAMES.adapter, "existing adapter is not the final MATCH action");
  smartrouteAssert(adapter.type === "socks5" && adapter.server === "127.0.0.1" && adapter.port === options.guardPort && adapter.udp === false,
    "existing Guard adapter does not match requested options");
  const expected = [
    [direct, options.directPort, "DIRECT"],
    [proxy, options.proxyPort, proxy.proxy],
    [original, options.originalPort, original.proxy],
  ];
  for (const [listener, port, target] of expected) {
    smartrouteAssert(listener.type === "mixed" && listener.listen === "127.0.0.1" && listener.port === port && listener.udp === false,
      "existing listener does not match requested options");
    smartrouteAssert(typeof target === "string" && target.length > 0, "existing listener is missing its forced path");
  }
  smartrouteAssert(indexes.groups.has(proxy.proxy) && indexes.groups.has(original.proxy), "existing forced group is missing");
  const proxyStats = smartrouteTerminalStats(proxy.proxy, indexes, new Set());
  const originalGroup = indexes.groups.get(original.proxy);
  smartrouteAssert(proxyStats.proxy > 0 && proxyStats.unknown === 0, "existing Proxy listener is not proxy-capable");
  smartrouteAssert(originalGroup && Array.isArray(originalGroup.proxies) && originalGroup.proxies.includes(proxy.proxy),
    "existing Original listener no longer owns the Proxy branch");
  return config;
}

function applySmartRoute(config, options) {
  smartrouteValidateOptions(options);
  smartrouteAssert(config && typeof config === "object", "config must be an object");
  smartrouteAssert(config.mode === "rule", "mode must be rule");
  smartrouteAssert(Array.isArray(config.rules), "rules must be an array");
  smartrouteAssert(Array.isArray(config.proxies), "proxies must be an array");
  smartrouteAssert(Array.isArray(config["proxy-groups"]), "proxy-groups must be an array");
  if (config.listeners === undefined) config.listeners = [];
  smartrouteAssert(Array.isArray(config.listeners), "listeners must be an array");

  const matches = [];
  for (let index = 0; index < config.rules.length; index += 1) {
    const parts = smartrouteParseRule(config.rules[index]);
    if (parts[0] === "MATCH") matches.push({ index, parts });
  }
  smartrouteAssert(matches.length === 1, "exactly one MATCH rule is required");
  const match = matches[0];
  smartrouteAssert(match.index === config.rules.length - 1, "MATCH must be the final rule");
  smartrouteAssert(match.parts.length === 2 && match.parts[1].length > 0, "final MATCH must have one action");

  const indexes = smartrouteIndexes(config);
  if (match.parts[1] === SMARTROUTE_NAMES.adapter) {
    return smartrouteVerifyExisting(config, options, indexes, match.parts);
  }

  smartrouteAssertPortIsolation(config, options);

  smartrouteAssert(!indexes.proxies.has(SMARTROUTE_NAMES.adapter) && !indexes.groups.has(SMARTROUTE_NAMES.adapter),
    "reserved adapter name collides with existing config");
  for (const name of [SMARTROUTE_NAMES.directListener, SMARTROUTE_NAMES.proxyListener, SMARTROUTE_NAMES.originalListener]) {
    smartrouteAssert(!smartrouteListener(config, name), `reserved listener name ${name} collides with existing config`);
  }
  const rootName = match.parts[1];
  const root = indexes.groups.get(rootName);
  smartrouteAssert(root && root.type === "select" && Array.isArray(root.proxies), "final MATCH action must be a select group");
  smartrouteAssert(root.proxies.length === 2, "final group must have exactly two branches");
  const branches = root.proxies.map((name) => ({ name, stats: smartrouteTerminalStats(name, indexes, new Set([rootName])) }));
  const directOnly = branches.filter(({ stats }) => stats.direct > 0 && stats.proxy === 0 && stats.reject === 0 && stats.unknown === 0);
  const proxyCapable = branches.filter(({ stats }) => stats.proxy > 0 && stats.reject === 0 && stats.unknown === 0);
  smartrouteAssert(directOnly.length === 1, "final group must have exactly one Direct-only branch");
  smartrouteAssert(proxyCapable.length === 1, "final group must have exactly one proxy-capable branch");
  smartrouteAssert(directOnly[0].name !== proxyCapable[0].name, "Direct and Proxy branches must be distinct");

  config.proxies.push({
    name: SMARTROUTE_NAMES.adapter,
    type: "socks5",
    server: "127.0.0.1",
    port: options.guardPort,
    udp: false,
  });
  config.listeners.push(
    { name: SMARTROUTE_NAMES.directListener, type: "mixed", listen: "127.0.0.1", port: options.directPort, proxy: "DIRECT", udp: false },
    { name: SMARTROUTE_NAMES.proxyListener, type: "mixed", listen: "127.0.0.1", port: options.proxyPort, proxy: proxyCapable[0].name, udp: false },
    { name: SMARTROUTE_NAMES.originalListener, type: "mixed", listen: "127.0.0.1", port: options.originalPort, proxy: rootName, udp: false },
  );
  config.rules[match.index] = `MATCH,${SMARTROUTE_NAMES.adapter}`;
  return config;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { applySmartRoute, SMARTROUTE_NAMES };
}
