import type { LLMProvider, LLMMessage, ToolDefinition } from "../llm/provider.ts";
import type { CapabilityRegistry } from "../capability/registry.ts";
import type { MemoryStore } from "../context/memory.ts";
import type { PatchManager } from "../patch/manager.ts";
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
  private autoReject = false;
  private consecutiveRejections = 0;
  private approvalResolvers: Map<string, (approved: boolean) => void> = new Map();

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
    for (const [, resolve] of this.approvalResolvers.entries()) {
      resolve(false);
    }
    this.approvalResolvers.clear();
  }

  async run(userMessage: string, options: { maxToolRounds?: number; autoApprove?: boolean; intent?: string; skills?: string[] } = {}): Promise<string> {
    const { maxToolRounds = 15, autoApprove = false, intent: rawIntent = "chat", skills = [] } = options;
    const intent = rawIntent.toUpperCase();
    this.aborted = false;
    this.autoApprove = autoApprove;
    this.autoReject = false;
    this.consecutiveRejections = 0;
    this.approvalResolvers.clear();

    const contextBlock = this.memory.getContextForLLM();
    const lastChanges = this.memory.getFileChanges();
    const changeBlock = lastChanges.created.length > 0 || lastChanges.modified.length > 0
      ? `\nRecent file changes:\nCreated: ${lastChanges.created.join(", ") || "none"}\nModified: ${lastChanges.modified.join(", ") || "none"}`
      : "";

    const skillBlock = skills.length > 0
      ? `\nActive Skills and Extra Instructions:\n${skills.join("\n\n")}`
      : "";

    let intentInstruction = "";
    if (intent === "CHAT") {
      intentInstruction = `
- The user is currently in CHAT mode. Focus on answering their questions or participating in conversation.
- AVOID using tools (like reading the whole codebase) for simple greetings or non-technical questions.
- Only read files if the user specifically asks about them or if you need specific code context to answer a question.`;
    } else if (intent === "CODE") {
      intentInstruction = `
- The user is in CODE mode. They likely want to make changes or fix something.
- Proactively explore to understand the task, but don't over-read files.`;
    }

    const systemPrompt = `You are RakitKode, an AI coding assistant with tools.

${contextBlock}${skillBlock}
${changeBlock}

### Project-Specific Skills & Knowledge Library
You have access to a deep library of frameworks in the ".rakitkode/skills/" directory.
1. Some skills (Type: manifest) are already in your prompt. Follow them strictly.
2. Many specialized skills are indexed as "Type: reference-only". These contain the actual implementation details (e.g. topic clusters, analytics models, ROI formulas).
3. If the user asks for a task that involves one of these domains, YOU MUST:
   - Identify the relevant Reference-Knowledge path.
   - Use the 'read_file' tool to read the latest content of that .md file.
   - ONLY then provide your plan and execution.

IMPORTANT - Tool Usage Rules:${intentInstruction}
- When a user mentions a filename like "test.md" or "@test.md", use the file's full resolved path.
- ALWAYS use write_file to create or modify files. Provide BOTH the "path" AND the full "content" of the new file.
- Do NOT use apply_patch — it is unreliable. Always use write_file with the complete file content.
- Do NOT call git_status, git_diff, or git_log unless the user explicitly asks about git or changes.
- Do NOT call run_tests or run_command unless the user explicitly asks to run tests or a command.

File path resolution hints:
- Use full paths when possible (e.g., "doc/test.md" not just "test.md").
- Use specialized knowledge from '.rakitkode/skills/' whenever available.

Be concise. No preamble. Execute multi-step plans without asking.`;

    const resolvedMessage = await this.resolveMentions(userMessage);

    const messages: LLMMessage[] = [
      { role: "system", content: systemPrompt },
      { role: "user", content: resolvedMessage },
    ];

    let finalContent = "";
    let lastChangedFile = "";
    let userRejected = false;

    try {
      for (let round = 0; round < maxToolRounds && !userRejected; round++) {
        if (this.aborted) {
          this.emitter.emit({ type: "error", data: { message: "Interrupted by user" } });
          break;
        }

        const toolDefs = this.getToolDefinitions();
        
        const response = await this.llm.chatStream(
          messages, 
          toolDefs, 
          this.emitter, 
          "rakitkode", 
          new AbortController().signal
        );

        if (this.aborted) break;

        const cleanContent = stripDeepSeekArtifacts(response.content || "");

        if (response.toolCalls && response.toolCalls.length > 0) {

          messages.push({
            role: "assistant",
            content: response.content || null,
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

            args = await this.resolvePathsInArgs(tc.name, args);

            this.emitter.emit({
              type: "tool_call_start",
              data: { id: tc.id, name: tc.name, input: args },
            });

            const needsApproval = tc.name === "write_file" || tc.name === "apply_patch" || tc.name === "delete_file" || tc.name === "run_command";
            
            let toolResult: Record<string, unknown>;
            let isError = false;

            if (needsApproval && !this.autoApprove) {
              if (this.autoReject) {
                this.emitter.emit({
                  type: "tool_call_output",
                  data: {
                    id: tc.id,
                    name: tc.name,
                    output: "Auto-rejected (user chose never)",
                    raw: "Error: User rejected all pending changes. Do NOT retry file modifications.",
                    error: true,
                  },
                });
                this.emitter.emit({
                  type: "tool_call_end",
                  data: { id: tc.id, name: tc.name },
                });
                messages.push({ role: "tool", content: "Error: User rejected all pending changes. Do NOT retry file modifications.", tool_call_id: tc.id });
                continue;
              }

              await this.showDiffPreview(tc.name, args, tc.id);
              
              this.emitter.emit({
                type: "tool_waiting_approval",
                data: { id: tc.id, name: tc.name },
              });
              const approved = await this.waitForApproval(tc.id);
              
              if (!approved) {
                this.consecutiveRejections++;

                if (this.consecutiveRejections >= 1) {
                  isError = true;
                  
                  this.emitter.emit({
                    type: "tool_call_output",
                    data: {
                      id: tc.id,
                      name: tc.name,
                      output: "Rejected by user",
                      raw: "Error: Action rejected by user. Do NOT retry. Stop and respond to the user.",
                      error: true,
                    },
                  });
                  this.emitter.emit({
                    type: "tool_call_end",
                    data: { id: tc.id, name: tc.name },
                  });
                  messages.push({ role: "tool", content: "Error: Action rejected by user. Do NOT retry file modifications. Stop and respond to the user.", tool_call_id: tc.id });

                  this.emitter.emit({ type: "done", data: {} });
                  userRejected = true;
                  break;
                }

                this.consecutiveRejections = 0;
              }
            }

            try {
              toolResult = await this.registry.execute(tc.name, args);
            } catch (err: unknown) {
              const message = err instanceof Error ? err.message : String(err);
              toolResult = { error: message };
              isError = true;
            }

            const formatted = formatToolOutput(tc.name, args, toolResult, this.memory);

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
    }

    return finalContent;
  }

  private waitForApproval(id: string): Promise<boolean> {
    return new Promise((resolve) => {
      this.approvalResolvers.set(id, resolve);
    });
  }

  public approve(id: string): void {
    const resolve = this.approvalResolvers.get(id);
    if (resolve) {
      resolve(true);
      this.approvalResolvers.delete(id);
    }
  }

  public reject(id: string): void {
    const resolve = this.approvalResolvers.get(id);
    if (resolve) {
      resolve(false);
      this.approvalResolvers.delete(id);
    }
  }

  public approveAll(): void {
    for (const [, resolve] of this.approvalResolvers.entries()) {
      resolve(true);
    }
    this.approvalResolvers.clear();
    this.autoApprove = true;
  }

  public rejectAll(): void {
    for (const [, resolve] of this.approvalResolvers.entries()) {
      resolve(false);
    }
    this.approvalResolvers.clear();
    this.autoReject = true;
  }

  private async showDiffPreview(toolName: string, args: Record<string, unknown>, tcId: string): Promise<void> {
    if (toolName === "apply_patch") {
      const diff = (args.diff as string) || "";
      if (!diff) return;
      
      this.emitter.emit({
        type: "tool_call_output",
        data: {
          id: `diff-${tcId}`,
          name: "diff_preview",
          output: diff,
          raw: diff,
          error: false,
          status: "pending",
        },
      });
      return;
    }

    if (toolName === "write_file") {
      const filePath = (args.path as string) || "";
      const content = (args.content as string) || "";
      if (!filePath || !content) return;

      const relPath = this.memory.getRelativePath(filePath);
      const absPath = (await this.memory.resolveFile(filePath)).path;
      let oldContent: string | null = null;
      try {
        const file = Bun.file(absPath);
        if (await file.exists()) {
          oldContent = await file.text();
        }
      } catch {}

      let preview: string;
      if (oldContent !== null) {
        const oldLines = oldContent.split("\n");
        const newLines = content.split("\n");
        const diffLines: string[] = [];
        diffLines.push(`--- a/${relPath}`);
        diffLines.push(`+++ b/${relPath}`);

        const maxLines = 60;
        let shown = 0;
        for (let i = 0; i < Math.max(oldLines.length, newLines.length) && shown < maxLines; i++) {
          const oldL = oldLines[i];
          const newL = newLines[i];
          if (oldL === newL) {
            diffLines.push(` ${oldL}`);
            shown++;
          } else {
            if (oldL !== undefined) {
              diffLines.push(`-${oldL}`);
              shown++;
            }
            if (newL !== undefined) {
              diffLines.push(`+${newL}`);
              shown++;
            }
          }
        }
        const totalChanges = Math.abs(newLines.length - oldLines.length) + newLines.filter((l, i) => i >= oldLines.length || l !== oldLines[i]).length;
        if (shown >= maxLines || totalChanges > maxLines) {
          diffLines.push(`@@ ... ${totalChanges} changed lines @@`);
        }
        preview = diffLines.join("\n");
      } else {
        const lines = content.split("\n");
        preview = [
          `--- a/${relPath}`,
          `+++ b/${relPath}`,
          `@@ -0,0 +1,${Math.min(lines.length, 30)} @@`,
          ...lines.slice(0, 30).map((l) => `+${l}`),
          ...(lines.length > 30 ? [`+... (${lines.length - 30} more lines)`] : []),
        ].join("\n");
      }

      this.emitter.emit({
        type: "tool_call_output",
        data: {
          id: `diff-${tcId}`,
          name: "diff_preview",
          output: preview,
          raw: preview,
          error: false,
          status: "pending",
        },
      });
    }
  }

  private extractFilePathFromDiff(diff: string): string {
    const match = diff.match(/^\+\+\+ [ab]\/(.+)$/m);
    return match ? match[1] : "";
  }

  private async resolveMentions(message: string): Promise<string> {
    const matches = Array.from(message.matchAll(/@(?:\[([^\]]+)\]|([^\s\[\]]+))/g));
    let result = message;
    
    for (const match of matches) {
      const fullMatch = match[0];
      const bracketed = match[1];
      const plain = match[2];
      const name = bracketed || plain;
      const fileEntity = await this.memory.resolveFile(name);
      if (fileEntity.found) {
        result = result.replace(fullMatch, `@${fileEntity.path}`);
      }
    }
    return result;
  }

  private async resolvePathsInArgs(
    toolName: string,
    args: Record<string, unknown>,
  ): Promise<Record<string, unknown>> {
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

      const fileEntity = await this.memory.resolveFile(val);
      if (fileEntity.found) {
        resolved[key] = fileEntity.path;
      }
    }

    if (this.isGitTool(toolName)) {
      resolved.cwd = this.memory.getProjectRoot();
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
