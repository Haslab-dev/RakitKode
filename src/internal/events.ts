import type { Patch, FileChange } from "../types.ts";
import type { LLMMessage } from "../internal/llm/provider.ts";

export type EventType =
  | "thinking_start"
  | "thinking_delta"
  | "thinking_end"
  | "tool_call_start"
  | "tool_call_output"
  | "tool_call_end"
  | "response_start"
  | "response_delta"
  | "response_end"
  | "token_usage"
  | "mode_change"
  | "yolo_change"
  | "error"
  | "done";

export interface StreamEvent {
  type: EventType;
  data: Record<string, unknown>;
}

export interface ThinkingData {
  agent: string;
  content: string;
}

export interface ToolCallData {
  id: string;
  name: string;
  input: Record<string, unknown>;
  output?: string;
  error?: string;
}

export interface TokenUsageData {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  model: string;
  duration: number;
}

export interface ResponseData {
  content: string;
  patches?: Patch[];
  fileChanges?: FileChange[];
}

export type EventCallback = (event: StreamEvent) => void;

export class EventEmitter {
  private listeners = new Map<EventType | "*", Set<EventCallback>>();

  on(type: EventType | "*", callback: EventCallback): () => void {
    if (!this.listeners.has(type)) {
      this.listeners.set(type, new Set());
    }
    this.listeners.get(type)!.add(callback);
    return () => this.listeners.get(type)?.delete(callback);
  }

  emit(event: StreamEvent): void {
    const specific = this.listeners.get(event.type);
    if (specific) {
      for (const cb of specific) cb(event);
    }
    const wildcard = this.listeners.get("*");
    if (wildcard) {
      for (const cb of wildcard) cb(event);
    }
  }

  removeAll(): void {
    this.listeners.clear();
  }
}
