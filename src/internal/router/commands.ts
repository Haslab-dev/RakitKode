import type { PatchManager } from "../patch/manager.ts";
import type { CapabilityRegistry } from "../capability/registry.ts";
import type { AgentLog } from "../../types.ts";

export interface CommandContext {
  patchManager: PatchManager;
  orchestrator: any;
  registry: CapabilityRegistry;
  addLog: (log: AgentLog) => void;
}

export interface CommandResult {
  output: string;
  success: boolean;
  action?: string;
}

export class CommandRouter {
  private context: CommandContext;

  constructor(context: CommandContext) {
    this.context = context;
  }

  async handle(input: string): Promise<CommandResult | null> {
    if (!input.startsWith("/")) return null;

    const parts = input.slice(1).split(" ");
    const command = parts[0].toLowerCase();
    const args = parts.slice(1).join(" ");

    switch (command) {
      case "agents":
        return this.handleAgents();
      case "tools":
        return this.handleTools();
      case "accept":
        return this.handleAccept(args);
      case "reject":
        return this.handleReject(args);
      case "accept-all":
        return this.handleAcceptAll();
      case "diff":
        return this.handleDiff();
      case "files":
        return this.handleFiles();
      case "run":
        return this.handleRun(args);
      case "logs":
        return this.handleLogs();
      case "yolo":
        return this.handleYolo();
      case "clear":
        return { output: "Screen cleared.", success: true, action: "clear" };
      case "help":
        return this.handleHelp();
      case "exit":
      case "quit":
        return { output: "Goodbye!", success: true, action: "exit" };
      default:
        return { output: `Unknown command: /${command}. Type /help for available commands.`, success: false };
    }
  }

  private handleYolo(): CommandResult {
    if (!this.context.orchestrator) {
      return { output: "Orchestrator not found.", success: false };
    }
    const current = !!this.context.orchestrator.isAutoApprove();
    const next = !current;
    this.context.orchestrator.setAutoApprove(next);
    if (next) {
      this.context.patchManager.acceptAll();
    }
    return { 
      output: `YOLO mode ${next ? "ENABLED" : "DISABLED"} 🚀`, 
      success: true,
      action: "yolo_change"
    };
  }

  private handleAgents(): CommandResult {
    return {
      output: "Available agents: planner, retriever, coder, reviewer, executor, fixer",
      success: true,
    };
  }

  private handleTools(): CommandResult {
    const tools = this.context.registry.list();
    const toolList = tools.map((t) => `  - ${t.name}: ${t.description}`).join("\n");
    return { output: `Available tools:\n${toolList}`, success: true };
  }

  private handleAccept(args: string): CommandResult {
    if (!args) {
      return { output: "Usage: /accept <patch-id>", success: false };
    }
    this.context.patchManager.acceptPatch(args);
    return { output: `Patch ${args} accepted.`, success: true };
  }

  private handleReject(args: string): CommandResult {
    if (!args) {
      return { output: "Usage: /reject <patch-id>", success: false };
    }
    this.context.patchManager.rejectPatch(args);
    return { output: `Patch ${args} rejected.`, success: true };
  }

  private handleAcceptAll(): CommandResult {
    this.context.patchManager.acceptAll();
    return { output: "All pending patches accepted.", success: true };
  }

  private handleDiff(): CommandResult {
    const patches = this.context.patchManager.getPendingPatches();
    if (patches.length === 0) {
      return { output: "No pending patches.", success: true };
    }
    const output = patches
      .map(
        (p) =>
          `[${p.id.substring(0, 8)}] ${p.filePath} (${p.hunks.length} hunks)`,
      )
      .join("\n");
    return { output, success: true };
  }

  private handleFiles(): CommandResult {
    const changes = this.context.patchManager.getChangesByType();
    let output = "";
    if (changes.modified.length > 0) {
      output += "Modified:\n" + changes.modified.map((f) => `  - ${f.path}`).join("\n") + "\n";
    }
    if (changes.created.length > 0) {
      output += "Created:\n" + changes.created.map((f) => `  - ${f.path}`).join("\n") + "\n";
    }
    if (changes.deleted.length > 0) {
      output += "Deleted:\n" + changes.deleted.map((f) => `  - ${f.path}`).join("\n") + "\n";
    }
    if (!output) output = "No file changes.";
    return { output, success: true };
  }

  private async handleRun(command: string): Promise<CommandResult> {
    if (!command) {
      return { output: "Usage: /run <command>", success: false };
    }
    try {
      const result = await this.context.registry.execute("run_command", {
        command,
      });
      const execResult = result as { stdout: string; stderr: string; exitCode: number };
      const output = [
        execResult.stdout || "",
        execResult.stderr || "",
        `Exit code: ${execResult.exitCode}`,
      ].filter(Boolean).join("\n");
      return { output, success: execResult.exitCode === 0 };
    } catch (err: any) {
      return { output: `Error: ${err.message}`, success: false };
    }
  }

  private handleLogs(): CommandResult {
    const logs = this.context.orchestrator.getAllLogs();
    if (logs.length === 0) {
      return { output: "No logs yet.", success: true };
    }
    const output = logs.slice(-20).map(
      (l: AgentLog) => `[${l.agent}] ${l.action}: ${l.detail}`,
    ).join("\n");
    return { output, success: true };
  }

  private handleHelp(): CommandResult {
    const commands = [
      "/agents - List available agents",
      "/tools - List available tools",
      "/diff - Show pending patches",
      "/files - Show file changes",
      "/accept <id> - Accept a patch",
      "/reject <id> - Reject a patch",
      "/accept-all - Accept all pending patches",
      "/run <cmd> - Run a shell command",
      "/logs - Show agent logs",
      "/yolo - Toggle auto-approve mode",
      "/clear - Clear screen",
      "/help - Show this help",
      "/exit - Exit RakitKode",
    ];
    return { output: commands.join("\n"), success: true };
  }
}
