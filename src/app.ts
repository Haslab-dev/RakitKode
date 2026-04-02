import type { TokenUsageData } from "./internal/events.ts";
import { MemoryStore } from "./internal/context/memory.ts";
import {
  CapabilityRegistry,
  OpenAIProvider,
  PlannerAgentRunner,
  IntentRouter,
  CommandRouter,
  PatchManager,
  EventEmitter,
  FileSystemTool,
  WriteFileTool,
  ListFilesTool,
  DeleteFileTool,
  ApplyPatchTool,
  GrepTool,
  SymbolSearchTool,
  RunCommandTool,
  RunTestsTool,
  GitDiffTool,
  GitStatusTool,
  GitCommitTool,
  GitCheckoutTool,
  GitLogTool,
} from "./internal/index.ts";

import type { ResolvedProviderConfig } from "./internal/llm/config.ts";
import { resolveProviderConfig } from "./internal/llm/config.ts";
import { loadProfile } from "./internal/llm/profile.ts";
import { MCPClient, MCPCapability } from "./internal/mcp/client.ts";
import { loadUserTools, loadSkills } from "./internal/tools/loader.ts";
import { join } from "path";

export interface RakitKodeConfig {
  apiKey?: string;
  baseURL?: string;
  model?: string;
  maxIterations?: number;
  workDir?: string;
  dbPath?: string;
  autoApprove?: boolean;
}

export class RakitKode {
  private registry: CapabilityRegistry;
  private llm: OpenAIProvider;
  private agentRunner: PlannerAgentRunner;
  private intentRouter: IntentRouter;
  private commandRouter: CommandRouter;
  private patchManager: PatchManager;
  private emitter: EventEmitter;
  private memory: MemoryStore;
  private lastUsage: TokenUsageData | null = null;
  private config: RakitKodeConfig;
  private mcpClients: MCPClient[] = [];
  private skills: string[] = [];

  constructor(config: RakitKodeConfig = {}) {
    this.config = config;
    const resolved = resolveProviderConfig({
      apiKey: config.apiKey,
      baseUrl: config.baseURL,
      model: config.model,
    });
    const workDir = config.workDir || process.cwd();

    this.emitter = new EventEmitter();
    this.llm = new OpenAIProvider(resolved);
    this.registry = new CapabilityRegistry();
    this.memory = new MemoryStore(workDir);
    this.patchManager = new PatchManager(this.memory);

    this.registerTools();

    this.agentRunner = new PlannerAgentRunner(this.llm, this.registry, this.emitter, this.memory, this.patchManager);

    this.intentRouter = new IntentRouter();
    this.intentRouter.setLLM(this.llm);

    this.commandRouter = new CommandRouter({
      patchManager: this.patchManager,
      orchestrator: this,
      registry: this.registry,
      addLog: () => {},
    });

    this.emitter.on("token_usage", (event) => {
      this.lastUsage = event.data as unknown as TokenUsageData;
    });
  }

  async bootstrap(): Promise<void> {
    const workDir = this.config.workDir || process.cwd();
    const configDir = join(workDir, ".rakitkode");
    
    // 1. Load User Tools
    await loadUserTools(this.registry, join(configDir, "tools"));
    
    // 2. Load Skills
    this.skills = await loadSkills(join(configDir, "skills"));
    
    // 3. Load MCP Servers from Profile
    const profile = loadProfile(workDir);
    if (profile && profile.mcpServers) {
      for (const [name, cfg] of Object.entries(profile.mcpServers)) {
        try {
          const client = new MCPClient(name, cfg);
          await client.start();
          this.mcpClients.push(client);
          
          const tools = await client.listTools();
          for (const t of tools) {
            this.registry.register(new MCPCapability(client, t.name, t.description, t.inputSchema));
          }
        } catch (err) {
          console.error(`Failed to start MCP server ${name}:`, err);
        }
      }
    }

    // Load extra skills from profile
    if (profile && profile.skills) {
      this.skills.push(...profile.skills);
    }
  }

  private registerTools(): void {
    const tools = [
      new FileSystemTool(),
      new WriteFileTool(),
      new ListFilesTool(),
      new DeleteFileTool(),
      new ApplyPatchTool(this.patchManager),
      new GrepTool(),
      new SymbolSearchTool(),
      new RunCommandTool(),
      new RunTestsTool(),
      new GitDiffTool(),
      new GitStatusTool(),
      new GitCommitTool(),
      new GitCheckoutTool(),
      new GitLogTool(),
    ];
    for (const tool of tools) {
      this.registry.register(tool);
    }
  }

  getEmitter(): EventEmitter {
    return this.emitter;
  }

  getLLM(): OpenAIProvider {
    return this.llm;
  }

  getMemory(): MemoryStore {
    return this.memory;
  }

  async processInput(input: string): Promise<void> {
    this.lastUsage = null;

    try {
      const { intent } = await this.intentRouter.detect(input);
      this.emitter.emit({ type: "mode_change", data: { mode: intent.toUpperCase() } });
      await this.agentRunner.run(input, { 
        intent, 
        autoApprove: this.isAutoApprove(),
        skills: this.skills,
      });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      this.emitter.emit({ type: "error", data: { message } });
    } finally {
      this.emitter.emit({ type: "done", data: {} });
    }
  }

  abort(): void {
    this.agentRunner.abort();
  }

  handlePatchAction(patchId: string, action: "accept" | "reject"): void {
    if (action === "accept") {
      this.patchManager.acceptPatch(patchId);
      this.agentRunner.approve(patchId);
    } else {
      this.patchManager.rejectPatch(patchId);
      this.agentRunner.reject(patchId);
    }
  }

  handleAllPatches(action: "accept" | "reject"): void {
    if (action === "accept") {
      this.patchManager.acceptAll();
      this.agentRunner.approveAll();
    } else {
      this.patchManager.rejectAll();
      this.agentRunner.rejectAll();
    }
  }

  async handleCommand(input: string): Promise<{ output: string; action?: string } | null> {
    const result = await this.commandRouter.handle(input);
    if (result && result.action === "yolo_change") {
      this.emitter.emit({ type: "yolo_change", data: { enabled: this.isAutoApprove() } });
    }
    return result ? { output: result.output, action: result.action } : null;
  }

  getLastUsage(): TokenUsageData | null {
    return this.lastUsage;
  }

  getModelName(): string {
    return this.llm.model;
  }

  getProviderName(): string {
    return this.llm.providerName;
  }

  getPatchManager(): PatchManager {
    return this.patchManager;
  }

  getRegistry(): CapabilityRegistry {
    return this.registry;
  }

  getCommandRouter(): CommandRouter {
    return this.commandRouter;
  }

  getSkills(): string[] {
    return this.skills;
  }

  getMCPClients(): MCPClient[] {
    return this.mcpClients;
  }

  isAutoApprove(): boolean {
    return this.config.autoApprove ?? false;
  }

  setAutoApprove(value: boolean): void {
    this.config.autoApprove = value;
  }

  getFileChanges(): { created: string[]; modified: string[]; deleted: string[] } {
    return this.memory.getFileChanges();
  }

  shutdown(): void {
    this.emitter.removeAll();
    for (const client of this.mcpClients) {
      client.stop();
    }
  }
}
