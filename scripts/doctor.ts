import { resolve } from "node:path";
import { existsSync } from "node:fs";
import { PROVIDERS, type ProviderId, isLocalProvider, detectProviderFromEnv } from "../src/internal/llm/providers.ts";
import { resolveProviderConfig } from "../src/internal/llm/config.ts";
import { hasLocalOllama, listOllamaModels } from "../src/internal/llm/ollama.ts";
import { loadProfile, profileExists, getProfilePath } from "../src/internal/llm/profile.ts";

interface CheckResult {
  name: string;
  status: "pass" | "warn" | "fail";
  message: string;
}

const results: CheckResult[] = [];

function pass(name: string, msg: string) {
  results.push({ name, status: "pass", message: msg });
}

function warn(name: string, msg: string) {
  results.push({ name, status: "warn", message: msg });
}

function fail(name: string, msg: string) {
  results.push({ name, status: "fail", message: msg });
}

async function checkNodeVersion() {
  const version = process.version;
  const major = Number.parseInt(version.slice(1), 10);
  if (major >= 20) pass("Node.js", `v${version}`);
  else fail("Node.js", `v${version} (requires >= 20)`);
}

async function checkBun() {
  if (typeof Bun !== "undefined") pass("Bun runtime", "detected");
  else warn("Bun runtime", "not detected (running on Node.js)");
}

async function checkProfile() {
  if (profileExists()) {
    const profile = loadProfile();
    if (profile) {
      pass("Provider profile", `${profile.provider} / ${profile.model} (${getProfilePath()})`);
    }
  } else {
    warn("Provider profile", "No .rakitkode-profile.json found. Run 'bun run profile:init' to create one.");
  }
}

async function checkProviderConfig() {
  const config = resolveProviderConfig();
  pass("Resolved provider", `${config.providerName} / ${config.model}`);

  if (isLocalProvider(config.providerId)) {
    pass("API key", "not required for local provider");
  } else if (config.apiKey) {
    const masked = `${config.apiKey.slice(0, 8)}...`;
    pass("API key", `set (${masked})`);
  } else {
    fail("API key", `missing for ${config.providerName}. Set ${PROVIDERS[config.providerId].envKey}`);
  }
}

async function checkProviderReachability() {
  const config = resolveProviderConfig();
  const url = config.baseURL;

  try {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 4000);
    const res = await fetch(url.replace(/\/v1$/, ""), { signal: ctrl.signal });
    clearTimeout(timer);
    if (res.ok || res.status === 401 || res.status === 403) {
      pass("Provider reachability", `${url} is reachable`);
    } else {
      warn("Provider reachability", `${url} returned ${res.status}`);
    }
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : String(err);
    fail("Provider reachability", `${url} - ${message}`);
  }
}

async function checkOllama() {
  if (await hasLocalOllama()) {
    pass("Ollama", "running at localhost:11434");
    const models = await listOllamaModels();
    if (models.length > 0) {
      const names = models.map((m) => m.name).join(", ");
      pass("Ollama models", `${models.length} installed: ${names}`);
    } else {
      warn("Ollama models", "no models installed. Run 'ollama pull llama3.1:8b'");
    }
  } else {
    warn("Ollama", "not running at localhost:11434");
  }
}

async function runChecks(): Promise<CheckResult[]> {
  await checkNodeVersion();
  await checkBun();
  await checkProfile();
  await checkProviderConfig();
  await checkProviderReachability();
  await checkOllama();
  return results;
}

function printResults(checks: CheckResult[]) {
  const icons: Record<string, string> = { pass: "✓", warn: "⚠", fail: "✗" };
  const colors: Record<string, string> = { pass: "\x1b[32m", warn: "\x1b[33m", fail: "\x1b[31m" };
  const reset = "\x1b[0m";

  for (const check of checks) {
    const icon = icons[check.status] || "?";
    const color = colors[check.status] || reset;
    console.log(`  ${color}${icon}${reset} ${check.name}: ${check.message}`);
  }

  const failed = checks.filter((c) => c.status === "fail").length;
  const warned = checks.filter((c) => c.status === "warn").length;
  const passed = checks.filter((c) => c.status === "pass").length;

  console.log();
  if (failed > 0) {
    console.log(`  \x1b[31m${failed} failed, ${warned} warnings, ${passed} passed\x1b[0m`);
    process.exit(1);
  } else if (warned > 0) {
    console.log(`  \x1b[33m${warned} warnings, ${passed} passed\x1b[0m`);
  } else {
    console.log(`  \x1b[32mAll ${passed} checks passed\x1b[0m`);
  }
}

async function main() {
  const args = process.argv.slice(2);
  const json = args.includes("--json");

  const checks = await runChecks();

  if (json) {
    console.log(JSON.stringify(checks, null, 2));
  } else {
    console.log("\n  RakitKode Runtime Diagnostics\n");
    printResults(checks);
  }
}

main();
