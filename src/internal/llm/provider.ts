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

export class OpenAIProvider implements LLMProvider {
  private apiKey: string;
  private baseURL: string;
  private modelName: string;
  readonly providerName: string;

  constructor(config: {
    apiKey: string;
    baseURL?: string;
    model: string;
  }) {
    this.apiKey = config.apiKey;
    this.baseURL = config.baseURL || "https://api.openai.com/v1";
    this.modelName = config.model;
    this.providerName = this.baseURL.includes("deepseek") ? "DeepSeek" : "OpenAI";
  }

  get model(): string {
    return this.modelName;
  }

  async chat(
    messages: LLMMessage[],
    tools?: ToolDefinition[],
  ): Promise<LLMResponse> {
    const body: Record<string, unknown> = { model: this.model, messages };
    if (tools?.length) body.tools = tools;

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

    const data = await response.json() as any;
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
      messages,
      stream: true,
    };
    if (tools?.length) {
      body.tools = tools;
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
    let toolCallsMap = new Map<string, { name: string; arguments: string }>();
    const decoder = new TextDecoder();
    let usage: { promptTokens: number; completionTokens: number; totalTokens: number } | undefined;

    try {
      while (true) {
        if (signal?.aborted) break;
        const { done, value } = await reader.read();
        if (done) break;

        const chunk = decoder.decode(value, { stream: true });
        const lines = chunk.split("\n").filter((l) => l.startsWith("data: "));

        for (const line of lines) {
          const data = line.slice(6);
          if (data === "[DONE]") continue;

          try {
            const parsed = JSON.parse(data);
            const delta = parsed.choices?.[0]?.delta;

            if (delta?.reasoning_content) {
              fullThinking += delta.reasoning_content;
            } else if (delta?.content) {
              if (!thinkingEnded) {
                thinkingEnded = true;
                if (fullThinking.trim()) {
                  emitter?.emit({ type: "thinking_end", data: { content: fullThinking.trim() } });
                }
              }
              rawContent += delta.content;
              emitter?.emit({ type: "response_delta", data: { content: "" } });
            }

            if (delta?.tool_calls) {
              if (!thinkingEnded) {
                thinkingEnded = true;
                if (fullThinking.trim()) {
                  emitter?.emit({ type: "thinking_end", data: { content: fullThinking.trim() } });
                }
              }
              for (const tc of delta.tool_calls) {
                const id = tc.id || `call_${tc.index}`;
                if (!toolCallsMap.has(id)) {
                  toolCallsMap.set(id, { name: "", arguments: "" });
                }
                const entry = toolCallsMap.get(id)!;
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
      toolCalls: [...toolCallsMap.entries()].map(([id, tc]) => ({
        id,
        name: tc.name,
        arguments: tc.arguments,
      })),
      usage,
    };
  }
}
