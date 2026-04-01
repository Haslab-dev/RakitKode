import type {
  AgentLog,
  Task,
  ToolCall,
  ExecutionResult,
} from "../../types.ts";
import type { LLMProvider, LLMMessage, ToolDefinition } from "../llm/provider.ts";
import type { CapabilityRegistry } from "../capability/registry.ts";
import { randomUUID } from "node:crypto";

export interface AgentConfig {
  name: string;
  role: string;
  systemPrompt: string;
  maxTokens?: number;
  temperature?: number;
}

export abstract class Agent {
  protected config: AgentConfig;
  protected llm: LLMProvider;
  protected registry: CapabilityRegistry;
  protected logs: AgentLog[] = [];

  constructor(config: AgentConfig, llm: LLMProvider, registry: CapabilityRegistry) {
    this.config = config;
    this.llm = llm;
    this.registry = registry;
  }

  abstract run(task: Task, context?: Record<string, unknown>): Promise<AgentResult>;

  protected log(action: string, detail: string): void {
    const entry: AgentLog = {
      id: randomUUID(),
      agent: this.config.name,
      action,
      detail,
      timestamp: new Date(),
    };
    this.logs.push(entry);
  }

  getLogs(): AgentLog[] {
    return [...this.logs];
  }

  clearLogs(): void {
    this.logs = [];
  }

  protected async callLLM(
    messages: LLMMessage[],
    tools?: ToolDefinition[],
  ): Promise<string> {
    this.log("llm_call", `Calling ${this.config.name} agent`);
    const response = await this.llm.chat(messages, tools);
    this.log("llm_response", response.content.substring(0, 200));
    return response.content;
  }

  protected async executeTool(
    name: string,
    input: Record<string, unknown>,
  ): Promise<Record<string, unknown>> {
    this.log("tool_call", `${name}: ${JSON.stringify(input).substring(0, 200)}`);
    try {
      const result = await this.registry.execute(name, input);
      this.log("tool_result", `${name}: success`);
      return result;
    } catch (err: any) {
      this.log("tool_error", `${name}: ${err.message}`);
      return { error: err.message };
    }
  }

  protected getToolDefinitions(): ToolDefinition[] {
    return this.registry
      .list()
      .map((cap) => ({
        type: "function" as const,
        function: {
          name: cap.name,
          description: cap.description,
          parameters: {
            type: "object",
            properties: {},
          },
        },
      }));
  }

  getName(): string {
    return this.config.name;
  }
}

export interface AgentResult {
  success: boolean;
  output: string;
  toolCalls?: ToolCall[];
  patches?: Array<{
    filePath: string;
    diff: string;
  }>;
  executionResult?: ExecutionResult;
  error?: string;
}

export class PlannerAgent extends Agent {
  constructor(llm: LLMProvider, registry: CapabilityRegistry) {
    super(
      {
        name: "planner",
        role: "Plan and break down tasks",
        systemPrompt: `You are a planning agent. Break down the user's task into clear, actionable steps.
Use tools to gather context about the codebase before planning.
Output a structured plan with numbered steps.`,
      },
      llm,
      registry,
    );
  }

  async run(task: Task): Promise<AgentResult> {
    this.log("start", `Planning task: ${task.description}`);

    const messages: LLMMessage[] = [
      { role: "system", content: this.config.systemPrompt },
      {
        role: "user",
        content: `Task: ${task.description}\n\nIntent: ${task.intent}\n\nCreate a step-by-step plan to accomplish this task.`,
      },
    ];

    const plan = await this.callLLM(messages, this.getToolDefinitions());
    this.log("complete", "Plan generated");

    return { success: true, output: plan };
  }
}

export class RetrieverAgent extends Agent {
  constructor(llm: LLMProvider, registry: CapabilityRegistry) {
    super(
      {
        name: "retriever",
        role: "Gather codebase context",
        systemPrompt: `You are a code context retrieval agent. Find relevant files, functions, and code patterns.
Use search and file tools to gather context. Return the most relevant code snippets.`,
      },
      llm,
      registry,
    );
  }

  async run(task: Task, context?: Record<string, unknown>): Promise<AgentResult> {
    this.log("start", `Retrieving context for: ${task.description}`);

    const plan = (context?.plan as string) || task.description;
    const messages: LLMMessage[] = [
      { role: "system", content: this.config.systemPrompt },
      {
        role: "user",
        content: `Task: ${task.description}\nPlan:\n${plan}\n\nFind relevant code context.`,
      },
    ];

    const contextResult = await this.callLLM(messages, this.getToolDefinitions());
    this.log("complete", "Context retrieved");

    return { success: true, output: contextResult };
  }
}

export class CoderAgent extends Agent {
  constructor(llm: LLMProvider, registry: CapabilityRegistry) {
    super(
      {
        name: "coder",
        role: "Generate code patches",
        systemPrompt: `You are a coding agent. Generate unified diff patches to implement changes.
IMPORTANT: Only use apply_patch tool for file changes. Never use write_file for existing files.
Always provide complete, correct unified diff format.
Think carefully about the existing code before making changes.`,
      },
      llm,
      registry,
    );
  }

  async run(task: Task, context?: Record<string, unknown>): Promise<AgentResult> {
    this.log("start", `Coding for: ${task.description}`);

    const plan = (context?.plan as string) || "";
    const codeContext = (context?.codeContext as string) || "";

    const messages: LLMMessage[] = [
      { role: "system", content: this.config.systemPrompt },
      {
        role: "user",
        content: `Task: ${task.description}\n${plan ? `Plan:\n${plan}\n` : ""}${codeContext ? `Code Context:\n${codeContext}\n` : ""}\nGenerate the necessary code changes using apply_patch.`,
      },
    ];

    const result = await this.callLLM(messages, this.getToolDefinitions());
    this.log("complete", "Code generated");

    return { success: true, output: result };
  }
}

export class ReviewerAgent extends Agent {
  constructor(llm: LLMProvider, registry: CapabilityRegistry) {
    super(
      {
        name: "reviewer",
        role: "Review and validate changes",
        systemPrompt: `You are a code review agent. Review proposed changes for correctness, safety, and quality.
Check for: bugs, security issues, missing edge cases, style consistency.
Use tools to verify the changes if needed.`,
      },
      llm,
      registry,
    );
  }

  async run(task: Task, context?: Record<string, unknown>): Promise<AgentResult> {
    this.log("start", `Reviewing changes for: ${task.description}`);

    const patches = (context?.patches as string) || "";
    const messages: LLMMessage[] = [
      { role: "system", content: this.config.systemPrompt },
      {
        role: "user",
        content: `Task: ${task.description}\n\nChanges:\n${patches}\n\nReview these changes. Are they correct and safe?`,
      },
    ];

    const review = await this.callLLM(messages);
    this.log("complete", "Review complete");

    const approved = review.toLowerCase().includes("approved") ||
      review.toLowerCase().includes("looks good") ||
      !review.toLowerCase().includes("issue") &&
      !review.toLowerCase().includes("problem") &&
      !review.toLowerCase().includes("error") &&
      !review.toLowerCase().includes("concern");

    return {
      success: true,
      output: review,
      error: approved ? undefined : "Review found issues",
    };
  }
}

export class ExecutorAgent extends Agent {
  constructor(llm: LLMProvider, registry: CapabilityRegistry) {
    super(
      {
        name: "executor",
        role: "Run commands and tests",
        systemPrompt: `You are an execution agent. Run tests and commands to validate changes.
Execute relevant test commands and report results.`,
      },
      llm,
      registry,
    );
  }

  async run(task: Task, context?: Record<string, unknown>): Promise<AgentResult> {
    this.log("start", `Executing for: ${task.description}`);

    const messages: LLMMessage[] = [
      { role: "system", content: this.config.systemPrompt },
      {
        role: "user",
        content: `Task: ${task.description}\n\nRun appropriate tests to verify the changes.`,
      },
    ];

    const result = await this.callLLM(messages, this.getToolDefinitions());
    this.log("complete", "Execution complete");

    return { success: true, output: result };
  }
}

export class FixerAgent extends Agent {
  constructor(llm: LLMProvider, registry: CapabilityRegistry) {
    super(
      {
        name: "fixer",
        role: "Fix errors and retry",
        systemPrompt: `You are a fix agent. Analyze errors and generate patches to fix them.
Focus on the specific error messages and stack traces.`,
      },
      llm,
      registry,
    );
  }

  async run(task: Task, context?: Record<string, unknown>): Promise<AgentResult> {
    this.log("start", `Fixing errors for: ${task.description}`);

    const error = (context?.error as string) || "Unknown error";
    const messages: LLMMessage[] = [
      { role: "system", content: this.config.systemPrompt },
      {
        role: "user",
        content: `Task: ${task.description}\n\nError:\n${error}\n\nFix this error using apply_patch.`,
      },
    ];

    const result = await this.callLLM(messages, this.getToolDefinitions());
    this.log("complete", "Fix generated");

    return { success: true, output: result };
  }
}
