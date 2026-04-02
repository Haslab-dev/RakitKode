import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { PROVIDERS, type ProviderId } from "./providers.ts";

export interface ProviderProfile {
  provider: ProviderId;
  apiKey?: string;
  baseURL?: string;
  model: string;
  createdAt: string;
}

const PROFILE_FILENAME = ".rakitkode-profile.json";

export function getProfilePath(cwd?: string): string {
  const dir = cwd || process.cwd();
  return resolve(dir, PROFILE_FILENAME);
}

export function loadProfile(cwd?: string): ProviderProfile | null {
  const path = getProfilePath(cwd);
  if (!existsSync(path)) return null;
  try {
    const raw = readFileSync(path, "utf-8");
    return JSON.parse(raw) as ProviderProfile;
  } catch {
    return null;
  }
}

export function saveProfile(profile: ProviderProfile, cwd?: string): void {
  const path = getProfilePath(cwd);
  writeFileSync(path, JSON.stringify(profile, null, 2));
}

export function deleteProfile(cwd?: string): void {
  const path = getProfilePath(cwd);
  if (existsSync(path)) {
    const { unlinkSync } = require("node:fs");
    unlinkSync(path);
  }
}

export function profileExists(cwd?: string): boolean {
  return existsSync(getProfilePath(cwd));
}

export function buildEnvFromProfile(profile: ProviderProfile): Record<string, string> {
  const env: Record<string, string> = {};
  const provider = PROVIDERS[profile.provider];

  env.RAKITKODE_PROVIDER = profile.provider;
  env.RAKITKODE_MODEL = profile.model;

  if (profile.apiKey) {
    env[provider.envKey] = profile.apiKey;
  }
  if (profile.baseURL) {
    env.RAKITKODE_BASE_URL = profile.baseURL;
  } else if (provider.baseURL) {
    env.RAKITKODE_BASE_URL = provider.baseURL;
  }

  return env;
}

export function buildLaunchEnv(profile?: ProviderProfile | null): Record<string, string> {
  const env = { ...process.env } as Record<string, string>;

  if (profile) {
    const profileEnv = buildEnvFromProfile(profile);
    for (const [key, value] of Object.entries(profileEnv)) {
      env[key] = value;
    }
  }

  return env;
}

export function createProfile(opts: {
  provider: ProviderId;
  model?: string;
  apiKey?: string;
  baseURL?: string;
}): ProviderProfile {
  const info = PROVIDERS[opts.provider];
  return {
    provider: opts.provider,
    apiKey: opts.apiKey,
    baseURL: opts.baseURL || info.baseURL,
    model: opts.model || info.defaultModel,
    createdAt: new Date().toISOString(),
  };
}
