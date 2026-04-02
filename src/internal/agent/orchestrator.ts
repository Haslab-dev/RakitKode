import type { Task, AgentLog } from "../../types.ts";
import type { LLMProvider } from "../llm/provider.ts";
import type { CapabilityRegistry } from "../capability/registry.ts";
import {
  Agent,
  PlannerAgent,
  RetrieverAgent,
  CoderAgent,
  ReviewerAgent,
  ExecutorAgent,
  FixerAgent,
  type AgentResult,
} from "./agents.ts";
import { PatchManager } from "../patch/manager.ts";

export interface AgentLoopConfig {
  maxIterations: number;
  autoReview: boolean;
  autoExecute: boolean;
  autoFix: boolean;
}

export class AgentOrchestrator {
  private agents: Map<string, Agent> = new Map();
  private patchManager: PatchManager;
  private config: AgentLoopConfig;
  private logs: AgentLog[] = [];
  private onLog?: (log: AgentLog) => void;

  constructor(
    llm: LLMProvider,
    registry: CapabilityRegistry,
    config?: Partial<AgentLoopConfig>,
  ) {
    this.patchManager = new PatchManager(undefined as never);
    this.config = {
      maxIterations: config?.maxIterations || 10,
      autoReview: config?.autoReview ?? true,
      autoExecute: config?.autoExecute ?? true,
      autoFix: config?.autoFix ?? true,
    };

    const agentConfigs = [
      new PlannerAgent(llm, registry),
      new RetrieverAgent(llm, registry),
      new CoderAgent(llm, registry),
      new ReviewerAgent(llm, registry),
      new ExecutorAgent(llm, registry),
      new FixerAgent(llm, registry),
    ];

    for (const agent of agentConfigs) {
      this.agents.set(agent.getName(), agent);
    }
  }

  onLogCallback(callback: (log: AgentLog) => void): void {
    this.onLog = callback;
  }

  private addLog(log: AgentLog): void {
    this.logs.push(log);
    this.onLog?.(log);
  }

  async run(task: Task): Promise<{
    success: boolean;
    output: string;
    iterations: number;
    logs: AgentLog[];
  }> {
    this.addLog({
      id: "",
      agent: "orchestrator",
      action: "start",
      detail: `Starting agent loop for: ${task.description}`,
      timestamp: new Date(),
    });

    let plan = "";
    let codeContext = "";
    let lastError = "";
    let success = false;
    let output = "";

    for (let i = 0; i < this.config.maxIterations; i++) {
      this.addLog({
        id: "",
        agent: "orchestrator",
        action: "iteration",
        detail: `Iteration ${i + 1}/${this.config.maxIterations}`,
        timestamp: new Date(),
      });

      // Phase 1: Plan (first iteration or on fix)
      if (i === 0 || (lastError && this.config.autoFix)) {
        const planner = this.agents.get("planner")!;
        const planResult = await planner.run(task);
        this.collectLogs(planner);
        plan = planResult.output;
      }

      // Phase 2: Retrieve context
      const retriever = this.agents.get("retriever")!;
      const contextResult = await retriever.run(task, { plan, codeContext });
      this.collectLogs(retriever);
      codeContext = contextResult.output;

      // Phase 3: Generate code
      const coder = this.agents.get("coder")!;
      const codeResult = await coder.run(task, { plan, codeContext });
      this.collectLogs(coder);

      // Phase 4: Review
      if (this.config.autoReview) {
        const reviewer = this.agents.get("reviewer")!;
        const reviewResult = await reviewer.run(task, { patches: codeResult.output });
        this.collectLogs(reviewer);

        if (reviewResult.error && !lastError) {
          lastError = `Review: ${reviewResult.error}`;
          continue;
        }
      }

      // Phase 5: Execute / Test
      if (this.config.autoExecute) {
        const executor = this.agents.get("executor")!;
        const execResult = await executor.run(task, { patches: codeResult.output });
        this.collectLogs(executor);

        if (execResult.executionResult && !execResult.executionResult.success) {
          lastError = execResult.executionResult.stderr || "Execution failed";
          if (this.config.autoFix) {
            const fixer = this.agents.get("fixer")!;
            const fixResult = await fixer.run(task, { error: lastError });
            this.collectLogs(fixer);
            lastError = "";
            output = fixResult.output;
            continue;
          }
        }
      }

      success = true;
      output = codeResult.output;
      break;
    }

    this.addLog({
      id: "",
      agent: "orchestrator",
      action: "complete",
      detail: success ? "Task completed successfully" : "Max iterations reached",
      timestamp: new Date(),
    });

    return { success, output, iterations: this.config.maxIterations, logs: this.logs };
  }

  private collectLogs(agent: Agent): void {
    for (const log of agent.getLogs()) {
      this.addLog(log);
    }
    agent.clearLogs();
  }

  getPatchManager(): PatchManager {
    return this.patchManager;
  }

  getAllLogs(): AgentLog[] {
    return [...this.logs];
  }

  clearLogs(): void {
    this.logs = [];
    for (const agent of this.agents.values()) {
      agent.clearLogs();
    }
  }
}
