export type PatchStatus = "pending" | "accepted" | "rejected";

export interface Hunk {
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: HunkLine[];
}

export interface HunkLine {
  type: "context" | "add" | "remove";
  content: string;
}

export interface Patch {
  id: string;
  filePath: string;
  hunks: Hunk[];
  status: PatchStatus;
  originalContent?: string;
  createdAt: Date;
}

export type FileChangeType = "created" | "modified" | "deleted";

export interface FileChange {
  path: string;
  type: FileChangeType;
}

export type Intent = "chat" | "code" | "plan" | "debug";

export interface Message {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  timestamp: Date;
  toolCalls?: ToolCall[];
}

export interface ToolCall {
  id: string;
  name: string;
  input: Record<string, unknown>;
  output?: unknown;
  error?: string;
}

export interface AgentLog {
  id: string;
  agent: string;
  action: string;
  detail: string;
  timestamp: Date;
}

export interface Task {
  id: string;
  description: string;
  status: "pending" | "running" | "completed" | "failed";
  intent: Intent;
  createdAt: Date;
  completedAt?: Date;
}

export interface ExecutionResult {
  exitCode: number;
  stdout: string;
  stderr: string;
  success: boolean;
}

export interface AppConfig {
  defaultModel: string;
  apiKey: string;
  baseURL: string;
  maxIterations: number;
  workDir: string;
  autoApprove: boolean;
}
