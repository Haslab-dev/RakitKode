export const PROVIDERS = {
  openai: {
    name: "OpenAI",
    baseURL: "https://api.openai.com/v1",
    envKey: "OPENAI_API_KEY",
    defaultModel: "gpt-4o",
    requiresKey: true,
  },
  deepseek: {
    name: "DeepSeek",
    baseURL: "https://api.deepseek.com",
    envKey: "DEEPSEEK_API_KEY",
    defaultModel: "deepseek-chat",
    requiresKey: true,
  },
  ollama: {
    name: "Ollama",
    baseURL: "http://localhost:11434/v1",
    envKey: "OLLAMA_API_KEY",
    defaultModel: "llama3.1:8b",
    requiresKey: false,
  },
  gemini: {
    name: "Gemini",
    baseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
    envKey: "GEMINI_API_KEY",
    defaultModel: "gemini-2.0-flash",
    requiresKey: true,
  },
  groq: {
    name: "Groq",
    baseURL: "https://api.groq.com/openai/v1",
    envKey: "GROQ_API_KEY",
    defaultModel: "llama-3.3-70b-versatile",
    requiresKey: true,
  },
  mistral: {
    name: "Mistral",
    baseURL: "https://api.mistral.ai/v1",
    envKey: "MISTRAL_API_KEY",
    defaultModel: "mistral-large-latest",
    requiresKey: true,
  },
  together: {
    name: "Together AI",
    baseURL: "https://api.together.xyz/v1",
    envKey: "TOGETHER_API_KEY",
    defaultModel: "meta-llama/Llama-3.3-70B-Instruct-Turbo",
    requiresKey: true,
  },
  openrouter: {
    name: "OpenRouter",
    baseURL: "https://openrouter.ai/api/v1",
    envKey: "OPENROUTER_API_KEY",
    defaultModel: "openai/gpt-4o",
    requiresKey: true,
  },
  azure: {
    name: "Azure OpenAI",
    baseURL: "",
    envKey: "AZURE_OPENAI_API_KEY",
    defaultModel: "gpt-4o",
    requiresKey: true,
  },
} as const;

export type ProviderId = keyof typeof PROVIDERS;

export interface ProviderInfo {
  name: string;
  baseURL: string;
  envKey: string;
  defaultModel: string;
  requiresKey: boolean;
}

export function getProvider(id: ProviderId): ProviderInfo {
  return PROVIDERS[id];
}

export function detectProviderFromURL(url: string): ProviderId {
  const lower = url.toLowerCase();
  if (lower.includes("deepseek")) return "deepseek";
  if (lower.includes("localhost") || lower.includes("127.0.0.1")) return "ollama";
  if (lower.includes("google") || lower.includes("gemini")) return "gemini";
  if (lower.includes("groq")) return "groq";
  if (lower.includes("mistral")) return "mistral";
  if (lower.includes("together")) return "together";
  if (lower.includes("openrouter")) return "openrouter";
  if (lower.includes("azure") || lower.includes("openai.azure")) return "azure";
  return "openai";
}

export function detectProviderFromEnv(): ProviderId | null {
  const env = process.env;
  if (env.RAKITKODE_PROVIDER) {
    const id = env.RAKITKODE_PROVIDER.toLowerCase() as ProviderId;
    if (id in PROVIDERS) return id;
  }
  if (env.DEEPSEEK_API_KEY) return "deepseek";
  if (env.GEMINI_API_KEY) return "gemini";
  if (env.GROQ_API_KEY) return "groq";
  if (env.MISTRAL_API_KEY) return "mistral";
  if (env.TOGETHER_API_KEY) return "together";
  if (env.OPENROUTER_API_KEY) return "openrouter";
  if (env.AZURE_OPENAI_API_KEY) return "azure";
  if (env.OPENAI_API_KEY) return "openai";
  if (env.RAKITKODE_API_KEY) return "openai";
  return null;
}

export function isLocalProvider(id: ProviderId): boolean {
  return id === "ollama";
}
