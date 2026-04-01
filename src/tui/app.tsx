import React, { useState, useEffect, useCallback, useRef } from "react";
import { Box, Text, useInput, useApp } from "ink";
import { EventEmitter, StreamEvent } from "../internal/events.ts";
import { TokenUsageData } from "../internal/events.ts";

export interface ChatEntry {
  id: string;
  type: "user" | "assistant" | "tool_call" | "tool_output" | "diff" | "diff_preview";
  content: string;
  name?: string;
  diffLines?: string[];
  diffStatus?: "pending" | "accepted" | "rejected";
  patchId?: string;
  isError?: boolean;
}

export interface TUIProps {
  emitter: EventEmitter;
  onSubmit: (input: string) => Promise<void>;
  onAbort: () => void;
  onExit: () => void;
  modelName: string;
  providerName: string;
  onAcceptDiff?: (id: string) => void;
  onAcceptAllDiffs?: () => void;
  onRejectDiff?: (id: string) => void;
}

function eid() {
  return Math.random().toString(36).substring(7);
}

function stripEnv(text: string): string {
  return text.replace(/<environment_details>[\s\S]*?<\/environment_details>/g, "").trim();
}

export const TUI: React.FC<TUIProps> = ({
  emitter,
  onSubmit,
  onAbort,
  onExit,
  modelName,
  providerName,
  onAcceptDiff,
  onAcceptAllDiffs,
  onRejectDiff,
}) => {
  const { exit } = useApp();
  const [entries, setEntries] = useState<ChatEntry[]>([]);
  const [input, setInput] = useState("");
  const inputStateRef = useRef("");

  useEffect(() => {
    inputStateRef.current = input;
  }, [input]);

  const [isProcessing, setIsProcessing] = useState(false);
  const [phase, setPhase] = useState<"idle" | "thinking" | "executing">("idle");
  const [currentToolName, setCurrentToolName] = useState("");
  const [lastUsage, setLastUsage] = useState<TokenUsageData | null>(null);
  const [currentMode, setCurrentMode] = useState("CHAT");
  const [pendingDiffs, setPendingDiffs] = useState<Map<string, string>>(new Map());
  const [isYolo, setIsYolo] = useState(false);
  
  const pendingEntriesRef = useRef<string[]>([]);
  const isProcessingRef = useRef(false);

  useEffect(() => {
    isProcessingRef.current = isProcessing;
  }, [isProcessing]);

  const addEntry = useCallback((entry: ChatEntry) => {
    setEntries((prev) => [...prev, entry]);
  }, []);

  useEffect(() => {
    const unsubs: (() => void)[] = [];
    
    unsubs.push(emitter.on("yolo_change", (ev: StreamEvent) => {
      setIsYolo(!!(ev.data as any).enabled);
    }));

    unsubs.push(emitter.on("thinking_start", () => {
      setPhase("thinking");
      setIsProcessing(true);
    }));

    unsubs.push(emitter.on("tool_call_start", (ev: StreamEvent) => {
      const { name } = ev.data as any;
      setPhase("executing");
      setCurrentToolName(name);
      addEntry({
        id: eid(),
        type: "tool_call",
        content: name,
        name,
      });
    }));

    unsubs.push(emitter.on("tool_call_output", (ev: StreamEvent) => {
      const { id, name, output, status } = ev.data as any;
      if (name === "diff_preview") {
        addEntry({
          id,
          type: "diff",
          content: output,
          diffStatus: status || "pending",
          patchId: id,
        });
        if (status !== "accepted") {
          setPendingDiffs((prev) => new Map(prev).set(id, output));
          pendingEntriesRef.current = [...pendingEntriesRef.current, id];
        }
      } else {
        addEntry({
          id: eid(),
          type: "tool_output",
          content: output,
          name,
        });
      }
    }));

    unsubs.push(emitter.on("response_end", (ev: StreamEvent) => {
      const { content } = ev.data as any;
      addEntry({
        id: eid(),
        type: "assistant",
        content: stripEnv(content),
      });
    }));

    unsubs.push(emitter.on("token_usage", (ev: StreamEvent) => {
      setLastUsage(ev.data as unknown as TokenUsageData);
    }));

    unsubs.push(emitter.on("mode_change", (ev: StreamEvent) => {
      setCurrentMode((ev.data as any).mode || "CHAT");
    }));

    unsubs.push(emitter.on("error", (ev: StreamEvent) => {
      addEntry({
        id: eid(),
        type: "assistant",
        content: (ev.data as any).message,
        isError: true,
      });
    }));

    unsubs.push(emitter.on("done", () => {
      setIsProcessing(false);
      setPhase("idle");
      setCurrentToolName("");
    }));

    return () => unsubs.forEach((u) => u());
  }, [emitter, addEntry]);

  const handleSubmit = useCallback((value: string) => {
    const trimmed = value.trim();
    if (!trimmed || isProcessing) return;

    const cmd = trimmed.toLowerCase();
    
    if (cmd === "/exit" || cmd === "/quit") { onExit(); exit(); return; }
    if (cmd === "/clear") { setEntries([]); setPendingDiffs(new Map()); setInput(""); return; }

    if (pendingDiffs.size > 0 && !isProcessing) {
      if (cmd === "aa" || trimmed === "A" || cmd === "/accept-all" || cmd === "accept-all") {
        const toAccept = [...pendingEntriesRef.current];
        if (toAccept.length > 0) {
          onAcceptAllDiffs?.();
          toAccept.forEach(id => {
            setEntries((prev) => prev.map((e) => e.id === id ? { ...e, diffStatus: "accepted" } : e));
          });
          setPendingDiffs(new Map());
          pendingEntriesRef.current = [];
          setInput("");
          return;
        }
      }

      if (cmd === "a" || cmd === "/accept" || cmd === "accept") {
        const id = pendingEntriesRef.current.at(-1);
        const entry = entries.find(e => e.id === id);
        if (id !== undefined && entry) {
          onAcceptDiff?.(entry.patchId || String(id));
          setEntries((prev) => prev.map((e) => e.id === id ? { ...e, diffStatus: "accepted" } : e));
          setPendingDiffs((prev) => { const n = new Map(prev); n.delete(id); return n; });
          pendingEntriesRef.current = pendingEntriesRef.current.filter((i) => i !== id);
          setInput("");
          return;
        }
      }

      if (cmd === "r" || cmd === "/reject" || cmd === "reject") {
        const id = pendingEntriesRef.current.at(-1);
        const entry = entries.find(e => e.id === id);
        if (id !== undefined && entry) {
          onRejectDiff?.(entry.patchId || String(id));
          setEntries((prev) => prev.map((e) => e.id === id ? { ...e, diffStatus: "rejected" } : e));
          setPendingDiffs((prev) => { const n = new Map(prev); n.delete(id); return n; });
          pendingEntriesRef.current = pendingEntriesRef.current.filter((i) => i !== id);
          setInput("");
          return;
        }
      }
    }

    setInput("");
    setIsProcessing(true);
    setPhase("thinking");
    setCurrentToolName("");
    addEntry({ id: eid(), type: "user", content: trimmed });
    onSubmit(trimmed);
  }, [isProcessing, onSubmit, onExit, exit, addEntry, pendingDiffs.size, onAcceptDiff, onAcceptAllDiffs, onRejectDiff, entries]);

  useInput((input, key) => {
    if (key.ctrl && input === "c") {
      if (isProcessingRef.current) {
        onAbort();
      } else {
        onExit();
        exit();
      }
      return;
    }

    if (isProcessing) return;

    if (key.return) {
      handleSubmit(inputStateRef.current);
      return;
    }

    if (key.backspace || key.delete) {
      setInput((prev) => prev.slice(0, -1));
      return;
    }

    if (input) {
      setInput((prev) => prev + input);
    }
  });

  return (
    <Box flexDirection="column" height="100%">
      <Box borderStyle="single" flexDirection="column" flexGrow={1} paddingX={1}>
        <Box marginBottom={1}>
          <Text bold color="cyan">RakitKode</Text>
          <Text color="gray"> | </Text>
          <Text color="white">{modelName} ({providerName})</Text>
          <Text color="gray"> | </Text>
          <Text bold color="yellow">{currentMode}</Text>
          {isYolo && (
            <Box marginLeft={1} paddingX={1} borderStyle="round" borderColor="red">
              <Text bold color="red">YOLO</Text>
            </Box>
          )}
        </Box>

        <Box flexDirection="column" flexGrow={1}>
          {entries.map((e) => (
            <Box key={e.id} flexDirection="column" marginBottom={1}>
              {e.type === "user" && (
                <Text color="green">❯ {e.content}</Text>
              )}
              {e.type === "assistant" && (
                <Text color={e.isError ? "red" : "white"}>{e.content}</Text>
              )}
              {e.type === "tool_call" && (
                <Text color="blue"> $ {e.content}</Text>
              )}
              {e.type === "diff" && (
                <Box borderStyle="single" borderColor={e.diffStatus === "pending" ? "yellow" : e.diffStatus === "accepted" ? "green" : "red"}>
                  <Text>{e.content}</Text>
                </Box>
              )}
            </Box>
          ))}
          {isProcessing && phase === "thinking" && (
            <Text color="gray"> Thinking...</Text>
          )}
          {isProcessing && phase === "executing" && (
            <Text color="gray"> Running {currentToolName}...</Text>
          )}
        </Box>

        {pendingDiffs.size > 0 && !isProcessing && (
          <Box borderStyle="round" borderColor="yellow" paddingX={1} marginBottom={1}>
            <Text bold color="yellow">{pendingDiffs.size} pending change(s). Accept (a) or Reject (r)?</Text>
          </Box>
        )}

        <Box borderStyle="single" paddingX={1}>
          <Text bold color="cyan">❯ </Text>
          <Text>{input}</Text>
          {!isProcessing && <Text color="cyan">_</Text>}
        </Box>
      </Box>
    </Box>
  );
};
