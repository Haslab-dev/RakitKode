const OLLAMA_BASE = "http://localhost:11434";

export interface OllamaModel {
  name: string;
  family: string;
  parameterSize: string;
  quantizationLevel: string;
  size: number;
}

export async function hasLocalOllama(timeoutMs = 1200): Promise<boolean> {
  try {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), timeoutMs);
    const res = await fetch(`${OLLAMA_BASE}/api/tags`, { signal: ctrl.signal });
    clearTimeout(timer);
    return res.ok;
  } catch {
    return false;
  }
}

export async function listOllamaModels(): Promise<OllamaModel[]> {
  try {
    const res = await fetch(`${OLLAMA_BASE}/api/tags`);
    if (!res.ok) return [];
    const data = (await res.json()) as { models?: Array<Record<string, unknown>> };
    return (data.models || []).map((m) => ({
      name: (m.name as string) || "",
      family: (m.details as Record<string, string>)?.family || "",
      parameterSize: (m.details as Record<string, string>)?.parameter_size || "",
      quantizationLevel: (m.details as Record<string, string>)?.quantization_level || "",
      size: (m.size as number) || 0,
    }));
  } catch {
    return [];
  }
}

export async function benchmarkOllamaModel(model: string): Promise<number | null> {
  try {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 15000);
    const start = Date.now();
    const res = await fetch(`${OLLAMA_BASE}/v1/chat/completions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model,
        messages: [{ role: "user", content: "Hi" }],
        max_tokens: 5,
        stream: false,
      }),
      signal: ctrl.signal,
    });
    clearTimeout(timer);
    if (!res.ok) return null;
    await res.json();
    return Date.now() - start;
  } catch {
    return null;
  }
}

export type Goal = "latency" | "balanced" | "coding";

interface ScoredModel {
  model: OllamaModel;
  score: number;
}

function parseParamBillion(paramStr: string): number {
  const match = paramStr.match(/([\d.]+)\s*[Bb]/);
  return match ? Number.parseFloat(match[1]) : 0;
}

function scoreModel(model: OllamaModel, goal: Goal): number {
  const params = parseParamBillion(model.parameterSize);
  let score = 50;
  const lower = model.name.toLowerCase();
  const isCoder = lower.includes("coder") || lower.includes("code");
  const isGeneral = lower.includes("llama") || lower.includes("qwen") || lower.includes("mistral") || lower.includes("gemma");

  switch (goal) {
    case "latency":
      if (params > 14) score -= 30;
      else if (params > 8) score -= 10;
      else if (params <= 4) score += 20;
      if (model.quantizationLevel.startsWith("Q4")) score += 10;
      if (isCoder) score -= 5;
      break;
    case "coding":
      if (isCoder) score += 30;
      if (params >= 7 && params <= 14) score += 15;
      if (params > 14) score += 5;
      if (params < 7) score -= 15;
      if (model.quantizationLevel.startsWith("Q8")) score += 10;
      if (isGeneral && !isCoder) score += 5;
      break;
    case "balanced":
      if (isCoder) score += 15;
      else if (isGeneral) score += 10;
      if (params >= 7 && params <= 14) score += 10;
      if (model.quantizationLevel.startsWith("Q4")) score += 5;
      break;
  }

  return Math.max(0, Math.min(100, score));
}

export async function recommendOllamaModel(
  goal: Goal = "balanced",
  benchmark = false,
): Promise<ScoredModel[]> {
  const models = await listOllamaModels();
  const chatModels = models.filter((m) => {
    const name = m.name.toLowerCase();
    return !name.includes("embed") && !name.includes("vision") && !name.includes("bge");
  });

  const scored = chatModels.map((m) => ({ model: m, score: scoreModel(m, goal) }));
  scored.sort((a, b) => b.score - a.score);

  if (benchmark && scored.length > 0) {
    const top3 = scored.slice(0, 3);
    for (const entry of top3) {
      const latency = await benchmarkOllamaModel(entry.model.name);
      if (latency !== null) {
        entry.score -= Math.floor(latency / 1000);
      }
    }
    scored.sort((a, b) => b.score - a.score);
  }

  return scored;
}
