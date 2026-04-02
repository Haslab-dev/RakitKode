import { PROVIDERS, type ProviderId, isLocalProvider } from "../src/internal/llm/providers.ts";
import { createProfile, saveProfile, loadProfile, profileExists } from "../src/internal/llm/profile.ts";
import { hasLocalOllama, recommendOllamaModel, type Goal } from "../src/internal/llm/ollama.ts";

const args = process.argv.slice(2);

function printUsage() {
  console.log(`
  Usage: bun run scripts/bootstrap.ts [options]

  Options:
    --provider <id>      Provider: openai, deepseek, ollama, gemini, groq, mistral, together, openrouter, azure
    --model <name>       Model name
    --api-key <key>      API key
    --base-url <url>     Custom base URL
    --goal <goal>        For ollama auto-select: latency, balanced, coding
    --auto               Auto-detect best available provider
    --list               List all available providers

  Examples:
    bun run scripts/bootstrap.ts --provider ollama --model llama3.1:8b
    bun run scripts/bootstrap.ts --provider openai --api-key sk-... --model gpt-4o
    bun run scripts/bootstrap.ts --provider ollama --goal coding
    bun run scripts/bootstrap.ts --auto
  `);
}

function parseArgs() {
  const parsed: Record<string, string | boolean> = {};
  for (let i = 0; i < args.length; i++) {
    if (args[i].startsWith("--")) {
      const key = args[i].slice(2);
      if (i + 1 < args.length && !args[i + 1].startsWith("--")) {
        parsed[key] = args[i + 1];
        i++;
      } else {
        parsed[key] = true;
      }
    }
  }
  return parsed;
}

function listProviders() {
  console.log("\n  Available providers:\n");
  for (const [id, info] of Object.entries(PROVIDERS)) {
    const keyHint = info.requiresKey ? `(env: ${info.envKey})` : "(local, no key needed)";
    console.log(`    ${id.padEnd(12)} ${info.name.padEnd(16)} ${info.defaultModel.padEnd(45)} ${keyHint}`);
  }
  console.log();
}

async function autoDetect(): Promise<{ provider: ProviderId; model: string } | null> {
  // Check for Ollama first
  if (await hasLocalOllama()) {
    const recommended = await recommendOllamaModel("balanced");
    if (recommended.length > 0) {
      return { provider: "ollama", model: recommended[0].model.name };
    }
  }

  // Check env vars for providers with keys
  const env = process.env;
  if (env.DEEPSEEK_API_KEY) return { provider: "deepseek", model: "deepseek-chat" };
  if (env.OPENAI_API_KEY) return { provider: "openai", model: "gpt-4o" };
  if (env.GEMINI_API_KEY) return { provider: "gemini", model: "gemini-2.0-flash" };
  if (env.GROQ_API_KEY) return { provider: "groq", model: "llama-3.3-70b-versatile" };
  if (env.MISTRAL_API_KEY) return { provider: "mistral", model: "mistral-large-latest" };

  return null;
}

async function main() {
  const parsed = parseArgs();

  if (parsed.list) {
    listProviders();
    return;
  }

  if (parsed.help || parsed.h) {
    printUsage();
    return;
  }

  let providerId = parsed.provider as ProviderId | undefined;
  let model = parsed.model as string | undefined;
  let apiKey = parsed.apiKey as string | undefined;
  const baseURL = parsed.base_url as string | undefined;
  const goal = (parsed.goal as Goal) || "balanced";

  // Auto mode
  if (parsed.auto) {
    console.log("\n  Auto-detecting best provider...\n");
    const detected = await autoDetect();
    if (detected) {
      providerId = detected.provider;
      model = detected.model;
      console.log(`  Detected: ${providerId} / ${model}\n`);
    } else {
      console.log("  No provider detected. Please specify --provider manually.\n");
      listProviders();
      process.exit(1);
    }
  }

  if (!providerId) {
    console.log("  Error: --provider is required. Use --auto to auto-detect.\n");
    printUsage();
    process.exit(1);
  }

  if (!(providerId in PROVIDERS)) {
    console.log(`  Error: Unknown provider "${providerId}".\n`);
    listProviders();
    process.exit(1);
  }

  // Ollama goal-based model selection
  if (providerId === "ollama" && !model) {
    console.log(`  Selecting best Ollama model for goal: ${goal}\n`);
    const recommended = await recommendOllamaModel(goal);
    if (recommended.length > 0) {
      model = recommended[0].model.name;
      console.log(`  Recommended: ${model} (score: ${recommended[0].score})\n`);
    } else {
      model = PROVIDERS.ollama.defaultModel;
      console.log(`  No models found, using default: ${model}\n`);
    }
  }

  // Validate key requirement
  if (isLocalProvider(providerId) && !apiKey) {
    console.log(`  Note: ${PROVIDERS[providerId].name} is a local provider, no API key needed.\n`);
  } else if (!isLocalProvider(providerId) && !apiKey) {
    const envKey = PROVIDERS[providerId].envKey;
    const envVal = process.env[envKey];
    if (envVal) {
      apiKey = envVal;
      console.log(`  Using API key from ${envKey}\n`);
    } else {
      console.log(`  Warning: No API key provided for ${PROVIDERS[providerId].name}.`);
      console.log(`  Set ${envKey} environment variable or pass --api-key.\n`);
    }
  }

  const profile = createProfile({ provider: providerId, model, apiKey, baseURL });
  saveProfile(profile);

  console.log("  Profile saved to .rakitkode-profile.json");
  console.log(`  Provider: ${PROVIDERS[providerId].name}`);
  console.log(`  Model: ${profile.model}`);
  console.log(`  Base URL: ${profile.baseURL}`);
  console.log(`  API Key: ${profile.apiKey ? "set" : "not set"}\n`);
  console.log("  Run 'bun run dev' to start RakitKode with this profile.\n");
}

main();
