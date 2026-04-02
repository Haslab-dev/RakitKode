import { PROVIDERS, type ProviderId, detectProviderFromURL, detectProviderFromEnv, isLocalProvider } from "./providers.ts";
import { loadProfile } from "./profile.ts";

function asTrimmedString(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

export interface ResolvedProviderConfig {
  apiKey: string;
  baseURL: string;
  model: string;
  providerName: string;
  providerId: ProviderId;
}

export function resolveProviderConfig(options?: {
  apiKey?: string;
  baseUrl?: string;
  model?: string;
}): ResolvedProviderConfig {
  const env = process.env;
  const profile = loadProfile();

  let providerId: ProviderId = "openai";
  let resolvedKey = options?.apiKey;
  let resolvedUrl = options?.baseUrl;
  let resolvedModel = options?.model;

  // Profile takes precedence
  if (profile) {
    providerId = profile.provider;
    if (profile.apiKey && !resolvedKey) resolvedKey = profile.apiKey;
    if (profile.baseURL && !resolvedUrl) resolvedUrl = profile.baseURL;
    if (profile.model && !resolvedModel) resolvedModel = profile.model;
  }

  // RAKITKODE_PROVIDER env
  if (!profile) {
    const detected = detectProviderFromEnv();
    if (detected) providerId = detected;
  }

  // Explicit provider env
  const envProvider = asTrimmedString(env.RAKITKODE_PROVIDER);
  if (envProvider && envProvider in PROVIDERS) {
    providerId = envProvider as ProviderId;
  }

  const providerInfo = PROVIDERS[providerId];

  // Resolve API key from provider-specific env if not set
  if (!resolvedKey) {
    resolvedKey = asTrimmedString(env[providerInfo.envKey]);
  }

  // Fallback chain for keys
  if (!resolvedKey) {
    resolvedKey = asTrimmedString(env.RAKITKODE_API_KEY)
      || asTrimmedString(env.OPENAI_API_KEY)
      || asTrimmedString(env.DEEPSEEK_API_KEY)
      || "";
  }

  // Resolve base URL
  if (!resolvedUrl) {
    resolvedUrl = asTrimmedString(env.RAKITKODE_BASE_URL)
      || asTrimmedString(env.OPENAI_BASE_URL)
      || providerInfo.baseURL;
  }

  // Resolve model
  if (!resolvedModel) {
    resolvedModel = asTrimmedString(env.RAKITKODE_MODEL)
      || asTrimmedString(env.OPENAI_MODEL)
      || providerInfo.defaultModel;
  }

  // Detect provider from URL if it contradicts
  if (resolvedUrl) {
    const urlDetected = detectProviderFromURL(resolvedUrl);
    if (urlDetected !== providerId) {
      providerId = urlDetected;
    }
  }

  // Validate: non-local providers need a key
  if (!isLocalProvider(providerId) && !resolvedKey) {
    console.error(`Warning: ${providerInfo.name} requires an API key. Set ${providerInfo.envKey} or use /profile to configure.`);
  }

  return {
    apiKey: resolvedKey || "",
    baseURL: resolvedUrl.replace(/\/+$/, ""),
    model: resolvedModel,
    providerName: providerInfo.name,
    providerId,
  };
}
