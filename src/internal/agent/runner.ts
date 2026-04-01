import type { EventEmitter } from "../events.ts";
import type { LLMProvider, LLMMessage, ToolDefinition } from "../llm/provider.ts";
import type { CapabilityRegistry } from "../capability/registry.ts";

function stripDeepSeekArtifacts(text: string): string {
  return text
    .replace(/<environment_details>[\s\S]*?<\/environment_details>/g, "")
    .replace(/<thinking>[\s\S]*?<\/thinking>/g, "")
    .replace(/<\/?environment_details>/g, "")
    .trim();
}

export class AgentRunner {
  private llm: LLMProvider;
  private registry: CapabilityRegistry;
  private emitter: EventEmitter;
  private aborted = false;
  private abortController: AbortController | null = null;

  constructor(llm: LLMProvider, registry: CapabilityRegistry, emitter: EventEmitter) {
    this.llm = llm;
    this.registry = registry;
    this.emitter = emitter;
  }

  abort(): void {
    this.aborted = true;
    this.abortController?.abort();
  }

  isAborted(): boolean {
    return this.aborted;
  }

  reset(): void {
    this.aborted = false;
    this.abortController = null;
  }

  async run(userMessage: string, systemPrompt: string, maxToolRounds = 10): Promise<string> {
    this.aborted = false;
    this.abortController = new AbortController();

    const messages: LLMMessage[] = [
      { role: "system", content: systemPrompt },
      { role: "user", content: userMessage },
    ];

    let finalContent = "";

    try {
      this.emitter.emit({ type: "thinking_start", data: { agent: "agent" } });

      for (let round = 0; round < maxToolRounds; round++) {
        if (this.aborted) {
          this.emitter.emit({ type: "error", data: { message: "Interrupted by user" } });
          break;
        }

      const toolDefs = this.getToolDefinitions();
      const response = await this.llm.chat(messages, toolDefs);

        if (this.aborted) break;

        const cleanContent = stripDeepSeekArtifacts(response.content || "");

        if (response.toolCalls && response.toolCalls.length > 0) {
          const assistantMsg: LLMMessage = {
            role: "assistant",
            content: null,
            tool_calls: response.toolCalls.map((tc) => ({
              id: tc.id,
              type: "function" as const,
              function: { name: tc.name || "", arguments: tc.arguments },
            })),
          };

          messages.push(assistantMsg);

          for (const tc of response.toolCalls) {
            if (this.aborted) {
              messages.push({ role: "tool", content: "Interrupted", tool_call_id: tc.id });
              break;
            }

            if (!tc.name || !this.registry.has(tc.name)) {
              const errOutput = tc.name
                ? `Error: Unknown tool "${tc.name}"`
                : "Error: Tool call missing name";
              messages.push({ role: "tool", content: errOutput, tool_call_id: tc.id });
              continue;
            }

            let args: Record<string, unknown> = {};
            try {
              args = JSON.parse(tc.arguments);
            } catch {
              args = { raw: tc.arguments };
            }

          this.emitter.emit({
            type: "tool_call_start",
            data: { id: tc.id, name: tc.name, input: args },
          });

          if (process.env.DEBUG_TOOL) {
            console.error("[TOOL CALL]", tc.name, "args_raw:", tc.arguments.substring(0, 300));
          }

            let output: string;
            let isError = false;

            try {
              const result = await this.registry.execute(tc.name, args);
              if (typeof result === "object" && result !== null) {
                if ("stdout" in result) {
                  const stdout = (result as any).stdout as string;
                  const stderr = (result as any).stderr as string;
                  output = [stdout, stderr].filter(Boolean).join("\n");
                } else {
                  output = JSON.stringify(result, null, 2);
                }
              } else {
                output = String(result);
              }
            } catch (err: any) {
              output = `Error: ${err.message}`;
              isError = true;
            }

            this.emitter.emit({
              type: "tool_call_output",
              data: { id: tc.id, name: tc.name, output, error: isError },
            });
            this.emitter.emit({
              type: "tool_call_end",
              data: { id: tc.id, name: tc.name },
            });

            messages.push({ role: "tool", content: output, tool_call_id: tc.id });
          }
        } else {
          finalContent = cleanContent || stripDeepSeekArtifacts(response.thinking || "");
          messages.push({ role: "assistant", content: finalContent || "" });

          if (finalContent) {
            this.emitter.emit({ type: "response_end", data: { content: finalContent } });
          }

          if (response.usage) {
            this.emitter.emit({
              type: "token_usage",
              data: {
                promptTokens: response.usage.promptTokens,
                completionTokens: response.usage.completionTokens,
                totalTokens: response.usage.totalTokens,
                model: "",
                duration: 0,
              },
            });
          }

          break;
        }
      }
    } finally {
      this.abortController = null;
    }

    return finalContent;
  }

  private getToolDefinitions(): ToolDefinition[] {
    return this.registry.list().map((cap) => {
      let params: Record<string, unknown> = {
        type: "object",
        properties: {},
        additionalProperties: false,
      };
      if (cap.parameters && typeof cap.parameters === "object") {
        params = { ...cap.parameters };
      }
      return {
        type: "function" as const,
        function: {
          name: cap.name,
          description: cap.description,
          parameters: params,
        },
      };
    });
  }
}
