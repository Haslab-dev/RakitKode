import type { LLMProvider, LLMMessage, ToolDefinition } from "../llm/provider.ts";
import type { CapabilityRegistry } from "../capability/registry.ts";
import type { MemoryStore } from "../context/memory.ts";
import type { PatchManager } from "../patch/manager.ts";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { EventEmitter } from "../events.ts";
import { formatToolOutput } from "../tools/formatter.ts";

function stripDeepSeekArtifacts(text: string): string {
  return text
    .replace(/<environment_details>[\s\S]*?<\/environment_details>/g, "")
    .replace(/<thinking>[\s\S]*?<\/thinking>/g, "")
    .replace(/<\/?environment_details>/g, "")
    .trim();
}

const CODE_EXTENSIONS = new Set([
  ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".go", ".rs",
  ".java", ".kt", ".rb", ".php", ".c", ".cpp", ".h", ".hpp", ".cs",
  ".swift", ".zig", ".nim", ".lua", ".r", ".jl", ".sh", ".bash",
]);

function isCodeFile(path: string): boolean {
  const ext = path.split(".").pop()?.toLowerCase() || "";
  return CODE_EXTENSIONS.has(`.${ext}`);
}

export class PlannerAgentRunner {
  private llm: LLMProvider;
  private registry: CapabilityRegistry;
  private emitter: EventEmitter;
  private memory: MemoryStore;
  private patchManager: PatchManager;
  private aborted = false;
  private autoApprove = false;

  constructor(
    llm: LLMProvider,
    registry: CapabilityRegistry,
    emitter: EventEmitter,
    memory: MemoryStore,
    patchManager: PatchManager,
  ) {
    this.llm = llm;
    this.registry = registry;
    this.emitter = emitter;
    this.memory = memory;
    this.patchManager = patchManager;
  }

  abort(): void {
    this.aborted = true;
  }

  async run(userMessage: string, options: { maxToolRounds?: number; autoApprove?: boolean } = {}): Promise<string> {
    const { maxToolRounds = 15, autoApprove = false } = options;
    this.aborted = false;
    this.autoApprove = autoApprove;

    const contextBlock = this.memory.getContextForLLM();
    const lastChanges = this.memory.getFileChanges();
    const changeBlock = lastChanges.created.length > 0 || lastChanges.modified.length > 0
      ? `\nRecent file changes:\nCreated: ${lastChanges.created.join(", ") || "none"}\nModified: ${lastChanges.modified.join(", ") || "none"}`
      : "";

    const systemPrompt = `You are RakitKode, an AI coding assistant with tools.

${contextBlock}
${changeBlock}

IMPORTANT - Tool Usage Rules:
- When a user mentions a filename like "test.md" or "@test.md", use the file's full resolved path.
- Prefer write_file for modifying files. Provide BOTH "path" AND "content" parameters.
- Only use apply_patch when making small surgical edits to a file you have already read. If apply_patch fails due to context mismatch, fall back to write_file with the full new file content.
- Do NOT call git_status, git_diff, or git_log unless the user explicitly asks about git or changes.
- Do NOT call run_tests or run_command unless the user explicitly asks to run tests or a command.

File path resolution hints:
- Use full paths when possible (e.g., "doc/test.md" not just "test.md").
- Check recent files above for previously accessed paths.
- If a file is referenced by name only, try the most likely location.

Be concise. No preamble. Execute multi-step plans without asking.`;

    const resolvedMessage = this.resolveMentions(userMessage);

    const messages: LLMMessage[] = [
      { role: "system", content: systemPrompt },
      { role: "user", content: resolvedMessage },
    ];

    let finalContent = "";
    let roundsSinceLastToolCall = 0;
    let emittedFinal = false;
    let lastChangedFile = "";

    try {
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
          roundsSinceLastToolCall = 0;

          messages.push({
            role: "assistant",
            content: null,
            tool_calls: response.toolCalls.map((tc) => ({
              id: tc.id,
              type: "function" as const,
              function: { name: tc.name || "", arguments: tc.arguments },
            })),
          });

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

            args = this.resolvePathsInArgs(tc.name, args);

            this.emitter.emit({
              type: "tool_call_start",
              data: { id: tc.id, name: tc.name, input: args },
            });

            this.emitDiffPreview(tc.name, args);

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

                if ("success" in result && (result as any).success === false) {
                  isError = true;
                }
              } else {
                output = String(result);
              }
            } catch (err: any) {
              output = `Error: ${err.message}`;
              isError = true;
            }

            const formatted = formatToolOutput(tc.name, args, output, this.memory);

            this.emitter.emit({
              type: "tool_call_output",
              data: {
                id: tc.id,
                name: tc.name,
                output: formatted.display,
                raw: formatted.raw,
                error: isError || !formatted.success,
              },
            });
            this.emitter.emit({
              type: "tool_call_end",
              data: { id: tc.id, name: tc.name },
            });

            messages.push({ role: "tool", content: formatted.raw, tool_call_id: tc.id });

            const actionSuccess = !isError && formatted.success;
            this.memory.recordAction(tc.name, args, formatted.display, actionSuccess);

            if (actionSuccess && (tc.name === "write_file" || tc.name === "apply_patch")) {
              const filePath = (tc.name === "write_file")
                ? (args.path as string)
                : this.extractFilePathFromDiff(args.diff as string);
              if (filePath && isCodeFile(filePath)) {
                lastChangedFile = filePath;
              }
            }
          }
        } else {
          finalContent = cleanContent || stripDeepSeekArtifacts(response.thinking || "");
          messages.push({ role: "assistant", content: finalContent || "" });

          if (finalContent && !emittedFinal) {
            this.emitter.emit({ type: "response_end", data: { content: finalContent } });
            emittedFinal = true;
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

          roundsSinceLastToolCall++;
          if (roundsSinceLastToolCall >= 2) break;
        }
      }
    } finally {
      // done is emitted by caller
    }

    return finalContent;
  }

  private emitDiffPreview(toolName: string, args: Record<string, unknown>): void {
    if (toolName === "apply_patch") {
      const diff = (args.diff as string) || "";
      if (diff) {
        const patches = this.patchManager.addPatchFromDiff(diff, this.autoApprove ? "accepted" : "pending");
        if (patches.length > 0) {
          this.emitter.emit({
            type: "tool_call_output",
            data: {
              id: patches[0].id,
              name: "diff_preview",
              output: diff,
              raw: diff,
              error: false,
              status: patches[0].status,
            },
          });
        }
      }
    }

    if (toolName === "write_file") {
      const filePath = (args.path as string) || "";
      const content = (args.content as string) || "";
      if (filePath && content) {
        const absPath = this.memory.resolveFile(filePath).path;
        let originalContent: string | undefined = undefined;
        try {
          if (absPath && existsSync(absPath)) {
            originalContent = readFileSync(absPath, "utf-8");
          }
        } catch {}

        const relPath = this.memory.getRelativePath(filePath);
        // Create a fake patch for preview and registration
        const diff = `--- a/${relPath}\n+++ b/${relPath}\n@@ -0,0 +1,${content.split("\n").length} @@\n${content.split("\n").map(l => "+" + l).join("\n")}`;
        const patches = this.patchManager.addPatchFromDiff(diff, this.autoApprove ? "accepted" : "pending", originalContent);
        
        const lines = content.split("\n").slice(0, 30);
        const preview = [
          `--- a/${relPath}`,
          `+++ b/${relPath}`,
          `@@ -0,0 +1,${lines.length} @@`,
          ...lines.map((l) => `+${l}`),
          ...(content.split("\n").length > 30 ? [`+... (${content.split("\n").length - 30} more lines)`] : []),
        ].join("\n");

        this.emitter.emit({
          type: "tool_call_output",
          data: {
            id: patches.length > 0 ? patches[0].id : `write-preview-${Date.now()}`,
            name: "diff_preview",
            output: preview,
            raw: preview,
            error: false,
            status: patches.length > 0 ? patches[0].status : "pending",
          },
        });
      }
    }
  }

  private extractFilePathFromDiff(diff: string): string {
    const match = diff.match(/^\+\+\+ [ab]\/(.+)$/m);
    return match ? match[1] : "";
  }

  private resolveMentions(message: string): string {
    // Support both @path and @[path]
    return message.replace(/@(?:\[([^\]]+)\]|([^\s\[\]]+))/g, (match, bracketed, plain) => {
      const name = bracketed || plain;
      const resolved = this.memory.resolveFile(name);
      if (resolved.found) {
        return `@${resolved.path}`;
      }
      return match;
    });
  }

  private resolvePathsInArgs(
    toolName: string,
    args: Record<string, unknown>,
  ): Record<string, unknown> {
    const pathKeys: Record<string, string[]> = {
      read_file: ["path"],
      write_file: ["path"],
      delete_file: ["path"],
      apply_patch: [],
      list_files: ["path"],
      grep: ["path"],
      symbol_search: ["path"],
      run_tests: ["cwd"],
      run_command: ["cwd"],
      git_diff: [],
      git_status: [],
      git_commit: [],
      git_checkout: [],
      git_log: [],
    };

    const keys = pathKeys[toolName];
    if (!keys) return args;

    const resolved = { ...args };

    for (const key of keys) {
      const val = args[key] as string | undefined;
      if (typeof val !== "string" || !val) continue;

      const existing = resolve(val);
      if (existing.includes("node_modules") || existing.includes(".rakitkode")) continue;

      const fileEntity = this.memory.resolveFile(val);
      if (fileEntity.found) {
        resolved[key] = fileEntity.path;
      }
    }

    if (this.isGitTool(toolName)) {
      resolved["cwd"] = this.memory.getProjectRoot();
    }

    return resolved;
  }

  private isGitTool(toolName: string): boolean {
    return toolName.startsWith("git_");
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
