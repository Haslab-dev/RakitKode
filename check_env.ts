const env = process.env;
console.log("RAKITKODE_API_KEY:", !!env.RAKITKODE_API_KEY);
console.log("DEEPSEEK_API_KEY:", !!env.DEEPSEEK_API_KEY);
console.log("OPENAI_API_KEY:", !!env.OPENAI_API_KEY);
console.log("RAKITKODE_BASE_URL:", env.RAKITKODE_BASE_URL);
console.log("OPENAI_BASE_URL:", env.OPENAI_BASE_URL);
console.log("RAKITKODE_MODEL:", env.RAKITKODE_MODEL);

import { resolveProviderConfig } from "./src/internal/llm/config.ts";
const config = resolveProviderConfig();
console.log("\nResolved Config:");
console.log("API Key present:", !!config.apiKey);
console.log("Base URL:", config.baseURL);
console.log("Model:", config.model);
console.log("Provider:", config.providerName);
