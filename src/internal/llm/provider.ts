import type { EventEmitter } from "../events.ts";

export interface LLMMessage {
  role: "system" | "user" | "assistant" | "tool";
  content: string | null;
  tool_calls?: Array<{
    id: string;
    type: "function";
    function: { name: string; arguments: string };
  }>;
  tool_call_id?: string;
  name?: string;
}

export interface LLMResponse {
  content: string;
  thinking?: string;
  toolCalls?: Array<{
    id: string;
    name: string;
    arguments: string;
  }>;
  usage?: {
    promptTokens: number;
    completionTokens: number;
    totalTokens: number;
  };
}

export interface ToolDefinition {
  type: "function";
  function: {
    name: string;
    description: string;
    parameters: Record<string, unknown>;
    strict?: boolean;
  };
}

export interface LLMProvider {
  readonly providerName: string;
  chat(messages: LLMMessage[], tools?: ToolDefinition[]): Promise<LLMResponse>;
  chatStream(
    messages: LLMMessage[],
    tools?: ToolDefinition[],
    emitter?: EventEmitter,
    agentName?: string,
    signal?: AbortSignal,
  ): Promise<LLMResponse>;
}

function cleanText(text: string): string {
  return text
    .replace(/<environment_details>[\s\S]*?<\/environment_details>/g, "")
    .replace(/<\/?environment_details>/g, "")
    .replace(/<thinking>[\s\S]*?<\/thinking>/g, "")
    .trim();
}

class ArtifactSuppressor {
  private inside = false;
  private pending = "";
  private static OPEN = /<(environment_details|thinking)>/;
  private static CLOSE = /<\/(environment_details|thinking)>/;
  private static MAX_TAG_LEN = 24;

  push(text: string): string {
    this.pending += text;
    let out = "";

    while (this.pending.length > 0) {
      if (this.inside) {
        const match = this.pending.match(ArtifactSuppressor.CLOSE);
        if (match) {
          this.inside = false;
          this.pending = this.pending.slice(match.index! + match[0].length);
        } else {
          this.pending = this.pending.slice(-ArtifactSuppressor.MAX_TAG_LEN);
          return out;
        }
      } else {
        const match = this.pending.match(ArtifactSuppressor.OPEN);
        if (match) {
          out += this.pending.slice(0, match.index!);
          this.inside = true;
          this.pending = this.pending.slice(match.index! + match[0].length);
        } else {
          const safeEnd = Math.max(0, this.pending.length - ArtifactSuppressor.MAX_TAG_LEN);
          out += this.pending.slice(0, safeEnd);
          this.pending = this.pending.slice(safeEnd);
          return out;
        }
      }
    }

    return out;
  }

  flush(): string {
    const remaining = this.pending;
    this.pending = "";
    this.inside = false;
    return cleanText(remaining);
  }
}

/**
 * OpenAI requires every key in `properties` to also appear in `required`.
 * Anthropic schemas often mark fields as optional (omitted from `required`),
 * which causes 400 errors on OpenAI/Codex endpoints. This normalizes the
 * schema by ensuring `required` is a superset of `properties` keys.
 */
function normalizeSchemaForOpenAI(
  schema: Record<string, unknown>,
  strict = true,
): Record<string, unknown> {
  if (schema.type !== "object" || !schema.properties) return schema;
  const properties = schema.properties as Record<string, unknown>;
  const existingRequired = Array.isArray(schema.required) ? (schema.required as string[]) : [];

  if (strict) {
    const allKeys = Object.keys(properties);
    const required = Array.from(new Set([...existingRequired, ...allKeys]));
    return { ...schema, required };
  }
  // For Gemini: keep only existing required keys that are present in properties
  const required = existingRequired.filter((k) => k in properties);
  return { ...schema, required };
}

export class OpenAIProvider implements LLMProvider {
  private apiKey: string;
  private baseURL: string;
  private modelName: string;
  readonly providerName: string;

  constructor(config: {
    apiKey: string;
    baseURL?: string;
    baseUrl?: string; // Support both casings
    model: string;
    providerName?: string;
  }) {
    this.apiKey = config.apiKey;
    this.baseURL = config.baseURL || config.baseUrl || "https://api.openai.com/v1";
    this.modelName = config.model;
    this.providerName = config.providerName || (this.baseURL.toLowerCase().includes("deepseek") ? "DeepSeek" : "OpenAI");
  }

  get model(): string {
    return this.modelName;
  }

  private normalizeTools(tools?: ToolDefinition[]): ToolDefinition[] | undefined {
    if (!tools?.length) return undefined;
    const isGemini = this.providerName === "Gemini";
    return tools.map((t) => ({
      ...t,
      function: {
        ...t.function,
        parameters: normalizeSchemaForOpenAI(t.function.parameters as Record<string, unknown>, !isGemini),
      },
    }));
  }

  private convertMessages(messages: LLMMessage[]): any[] {
    // Standardize roles and content
    return messages.map((m) => {
      const msg: any = { role: m.role, content: m.content };
      if (m.tool_calls) msg.tool_calls = m.tool_calls;
      if (m.tool_call_id) msg.tool_call_id = m.tool_call_id;
      if (m.name) msg.name = m.name;
      return msg;
    });
  }

  async chat(
    messages: LLMMessage[],
    tools?: ToolDefinition[],
  ): Promise<LLMResponse> {
    const body: Record<string, unknown> = {
      model: this.model,
      messages: this.convertMessages(messages),
    };
    const normalizedTools = this.normalizeTools(tools);
    if (normalizedTools?.length) body.tools = normalizedTools;

    const response = await fetch(`${this.baseURL}/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${this.apiKey}`,
      },
      body: JSON.stringify(body),
    });

    if (!response.ok) {
      const error = await response.text();
      throw new Error(`LLM API error (${response.status}): ${error}`);
    }

    const data = (await response.json()) as any;
    const msg = data.choices?.[0]?.message || {};

    return {
      content: cleanText(msg.content || ""),
      thinking: msg.reasoning_content || undefined,
      toolCalls: msg.tool_calls?.map((tc: any) => ({
        id: tc.id,
        name: tc.function.name,
        arguments: tc.function.arguments,
      })),
      usage: data.usage
        ? {
            promptTokens: data.usage.prompt_tokens,
            completionTokens: data.usage.completion_tokens,
            totalTokens: data.usage.total_tokens,
          }
        : undefined,
    };
  }

  async chatStream(
    messages: LLMMessage[],
    tools?: ToolDefinition[],
    emitter?: EventEmitter,
    agentName?: string,
    signal?: AbortSignal,
  ): Promise<LLMResponse> {
    const body: Record<string, unknown> = {
      model: this.model,
      messages: this.convertMessages(messages),
      stream: true,
      stream_options: { include_usage: true },
    };
    const normalizedTools = this.normalizeTools(tools);
    if (normalizedTools?.length) {
      body.tools = normalizedTools;
    }

    const startTime = Date.now();
    emitter?.emit({ type: "thinking_start", data: { agent: agentName || "agent" } });

    const response = await fetch(`${this.baseURL}/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${this.apiKey}`,
      },
      body: JSON.stringify(body),
      signal,
    });

    if (signal?.aborted) {
      emitter?.emit({ type: "error", data: { message: "Aborted" } });
      return { content: "", thinking: undefined, toolCalls: [] };
    }

    if (!response.ok) {
      const error = await response.text();
      emitter?.emit({ type: "error", data: { message: error } });
      throw new Error(`LLM API error (${response.status}): ${error}`);
    }

    const reader = response.body?.getReader();
    if (!reader) throw new Error("No response body");

    let rawContent = "";
    let fullThinking = "";
    let thinkingEnded = false;
    let toolCallsMap = new Map<number, { id: string; name: string; arguments: string }>();
    const decoder = new TextDecoder();
    let usage: { promptTokens: number; completionTokens: number; totalTokens: number } | undefined;
    let buffer = "";
    const suppressor = new ArtifactSuppressor();

    try {
      while (true) {
        if (signal?.aborted) break;
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || ""; // Keep the last incomplete line in the buffer

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed || !trimmed.startsWith("data: ")) continue;
          
          const data = trimmed.slice(6);
          if (data === "[DONE]") continue;

          try {
            const parsed = JSON.parse(data);
            const delta = parsed.choices?.[0]?.delta;

            if (delta?.reasoning_content) {
              fullThinking += delta.reasoning_content;
              emitter?.emit({ type: "thinking_delta", data: { content: delta.reasoning_content } });
            } else if (delta?.content) {
              if (!thinkingEnded) {
                thinkingEnded = true;
                if (fullThinking.trim()) {
                  emitter?.emit({ type: "thinking_end", data: { content: fullThinking.trim() } });
                }
              }
              const cleaned = suppressor.push(delta.content);
              if (cleaned) {
                rawContent += cleaned;
                emitter?.emit({ type: "response_delta", data: { content: cleaned } });
              }
            }

            if (delta?.tool_calls) {
              if (!thinkingEnded) {
                thinkingEnded = true;
                if (fullThinking.trim()) {
                  emitter?.emit({ type: "thinking_end", data: { content: fullThinking.trim() } });
                }
              }
              for (const tc of delta.tool_calls) {
                const index = tc.index;
                if (!toolCallsMap.has(index)) {
                  toolCallsMap.set(index, { id: tc.id || "", name: "", arguments: "" });
                }
                const entry = toolCallsMap.get(index)!;
                if (tc.id) entry.id = tc.id;
                if (tc.function?.name) entry.name += tc.function.name;
                if (tc.function?.arguments) entry.arguments += tc.function.arguments;
              }
            }

            if (parsed.usage) {
              usage = {
                promptTokens: parsed.usage.prompt_tokens,
                completionTokens: parsed.usage.completion_tokens,
                totalTokens: parsed.usage.total_tokens,
              };
            }
          } catch {
            // skip malformed chunks
          }
        }
      }
    } catch (err: any) {
      if (err.name !== "AbortError") throw err;
    }

    if (!thinkingEnded && fullThinking.trim()) {
      emitter?.emit({ type: "thinking_end", data: { content: fullThinking.trim() } });
    }

    reader.releaseLock();

    const flushed = suppressor.flush();
    if (flushed) {
      rawContent += flushed;
    }

    const finalContent = cleanText(rawContent);

    if (finalContent) {
      emitter?.emit({ type: "response_end", data: { content: finalContent } });
    }

    const duration = Date.now() - startTime;
    emitter?.emit({
      type: "token_usage",
      data: {
        promptTokens: usage?.promptTokens || 0,
        completionTokens: usage?.completionTokens || 0,
        totalTokens: usage?.totalTokens || 0,
        model: this.model,
        duration,
      },
    });

    return {
      content: finalContent,
      thinking: fullThinking.trim() || undefined,
      toolCalls: Array.from(toolCallsMap.values()).map((tc) => ({
        id: tc.id,
        name: tc.name,
        arguments: tc.arguments,
      })),
      usage,
    };
  }
}

