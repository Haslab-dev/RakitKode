import React, { useState, useEffect, useRef, useMemo } from "react";
import { Box, Text, useInput, useApp } from "ink";
import TextInput from "ink-text-input";
import type { EventEmitter, TokenUsageData } from "../internal/events.ts";

// ── Types ───────────────────────────────────────────────────────────────────

export interface ChatEntry {
  id: string;
  type: "user" | "assistant" | "tool_call" | "tool_output" | "diff" | "system";
  content: string;
  name?: string;
  status?: "pending" | "thinking" | "executing" | "waiting" | "done" | "accepted" | "rejected";
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
  onRejectAllDiffs?: () => void;
}

// ── Command Palette ────────────────────────────────────────────────────────

const COMMANDS = [
  { cmd: "/help", desc: "Show available commands" },
  { cmd: "/yolo", desc: "Toggle auto-approve mode" },
  { cmd: "/diff", desc: "Show pending patches" },
  { cmd: "/files", desc: "Show modified files" },
  { cmd: "/models", desc: "Show current provider and model" },
  { cmd: "/tools", desc: "List available tools" },
  { cmd: "/accept", desc: "Accept a specific patch" },
  { cmd: "/reject", desc: "Reject a specific patch" },
  { cmd: "/accept-all", desc: "Accept all pending patches" },
  { cmd: "/run", desc: "Run a shell command" },
  { cmd: "/doctor", desc: "Runtime diagnostics" },
  { cmd: "/new", desc: "New session (clear history)" },
  { cmd: "/clear", desc: "Clear terminal screen" },
  { cmd: "/exit", desc: "Exit RakitKode" },
];

const CommandPalette: React.FC<{
  query: string;
  onSelect: (cmd: string) => void;
  onClose: () => void;
  visible: boolean;
}> = ({ query, onSelect, onClose, visible }) => {
  const [selectedIndex, setSelectedIndex] = useState(0);
  const filtered = useMemo(() => {
    const q = query.toLowerCase();
    return q
      ? COMMANDS.filter((c) => c.cmd.includes(q) || c.desc.toLowerCase().includes(q))
      : COMMANDS;
  }, [query]);

  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  useInput((_data, key) => {
    if (!visible) return;
    if (key.escape) { onClose(); return; }
    if (key.upArrow) { setSelectedIndex((i) => Math.max(0, i - 1)); return; }
    if (key.downArrow) { setSelectedIndex((i) => Math.min(filtered.length - 1, i + 1)); return; }
    if (key.return && filtered[selectedIndex]) { onSelect(filtered[selectedIndex].cmd); return; }
  });

  if (!visible || filtered.length === 0) return null;

  return (
    <Box flexDirection="column" marginTop={0}>
      {filtered.slice(0, 14).map((c, i) => (
        <Box key={c.cmd}>
          <Text color={i === selectedIndex ? "cyan" : "gray"} bold={i === selectedIndex}>
            {i === selectedIndex ? "> " : "  "}
          </Text>
          <Text bold color={i === selectedIndex ? "white" : "gray"}>
            {c.cmd.padEnd(14)}
          </Text>
          <Text color={i === selectedIndex ? "white" : "gray"}>{c.desc}</Text>
        </Box>
      ))}
    </Box>
  );
};

// ── Diff View Component ────────────────────────────────────────────────────

const DiffView: React.FC<{ content: string; status?: string }> = ({ content, status }) => {
  const lines = content.split("\n");
  const borderColor = status === "accepted" ? "green" : status === "rejected" ? "red" : "yellow";
  
  return (
    <Box flexDirection="column" borderStyle="single" borderColor={borderColor} paddingX={1} marginY={0}>
      {lines.map((line, i) => {
        if (line.startsWith("+++")) {
          return <Text key={i} color="cyan">{line}</Text>;
        }
        if (line.startsWith("---")) {
          return <Text key={i} color="cyan">{line}</Text>;
        }
        if (line.startsWith("@@")) {
          return <Text key={i} color="magenta">{line}</Text>;
        }
        if (line.startsWith("+")) {
          return <Text key={i} color="green">{line}</Text>;
        }
        if (line.startsWith("-")) {
          return <Text key={i} color="red">{line}</Text>;
        }
        return <Text key={i} dimColor>{line}</Text>;
      })}
    </Box>
  );
};

// ── Tool Call Component ────────────────────────────────────────────────────

const ToolCallView: React.FC<{ entry: ChatEntry }> = ({ entry }) => {
  const isActive = entry.status === "executing" || entry.status === "thinking";
  const isWaiting = entry.status === "waiting";
  const color = isWaiting ? "yellow" : isActive ? "cyan" : "gray";

  const argsStr = entry.content;
  const truncated = argsStr.length > 120 ? `${argsStr.slice(0, 120)}...` : argsStr;

  return (
    <Box flexDirection="column" marginTop={0} marginBottom={0}>
      <Box>
        <Text dimColor>  </Text>
        <Text color={color} bold>{entry.name}</Text>
        <Text dimColor> {truncated}</Text>
      </Box>
    </Box>
  );
};

// ── Approval Banner ────────────────────────────────────────────────────────

const ApprovalBanner: React.FC<{
  onAccept: () => void;
  onReject: () => void;
  onAcceptAll: () => void;
  onRejectAll: () => void;
  toolName: string;
}> = ({ onAccept, onReject, onAcceptAll, onRejectAll, toolName }) => (
  <Box borderStyle="round" borderColor="yellow" paddingX={1} marginY={0}>
    <Box>
      <Text bold color="yellow">Allow {toolName}?</Text>
      <Box flexGrow={1} />
      <Text dimColor> </Text>
      <Text bold color="green">y</Text>
      <Text dimColor>/</Text>
      <Text bold color="red">n</Text>
      <Text dimColor>{" │ "}</Text>
      <Text bold color="green">yes</Text>
      <Text dimColor>/</Text>
      <Text bold color="green">yy</Text>
      <Text dimColor>{" always │ "}</Text>
      <Text bold color="red">no</Text>
      <Text dimColor>/</Text>
      <Text bold color="red">nn</Text>
      <Text dimColor>{" never "}</Text>
    </Box>
  </Box>
);

// ── Helper ───────────────────────────────────────────────────────────────────

function eid() {
  return Math.random().toString(36).substring(7);
}

// ── Main TUI Component ─────────────────────────────────────────────────────

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
  onRejectAllDiffs,
}) => {
  const { exit } = useApp();
  
  const [entries, setEntries] = useState<ChatEntry[]>([]);
  const [input, setInput] = useState("");
  const [isProcessing, setIsProcessing] = useState(false);
  const [currentTool, setCurrentTool] = useState("");
  const [mode, setMode] = useState("CHAT");
  const [isYolo, setIsYolo] = useState(false);
  const [usage, setUsage] = useState<TokenUsageData | null>(null);
  const [streamingResponse, setStreamingResponse] = useState("");
  const [streamingThinking, setStreamingThinking] = useState("");
  const [waitingApproval, setWaitingApproval] = useState<{ id: string; name: string } | null>(null);
  const [showPalette, setShowPalette] = useState(false);

  const inputRef = useRef("");
  const processingRef = useRef(false);
  const approvalRef = useRef<{ id: string; name: string } | null>(null);

  useEffect(() => { inputRef.current = input; }, [input]);
  useEffect(() => { processingRef.current = isProcessing; }, [isProcessing]);
  useEffect(() => { approvalRef.current = waitingApproval; }, [waitingApproval]);

  useEffect(() => {
    const unsubs: (() => void)[] = [];

    unsubs.push(emitter.on("mode_change", (ev) => setMode((ev.data as Record<string, string>).mode)));
    unsubs.push(emitter.on("yolo_change", (ev) => setIsYolo(!!(ev.data as Record<string, boolean>).enabled)));
    unsubs.push(emitter.on("token_usage", (ev) => setUsage(ev.data as unknown as TokenUsageData)));

    unsubs.push(emitter.on("thinking_start", () => {
      setStreamingThinking("");
    }));
    
    unsubs.push(emitter.on("thinking_delta", (ev) => {
      setStreamingThinking(prev => prev + (ev.data as Record<string, string>).content);
    }));

    unsubs.push(emitter.on("thinking_end", () => {
      setStreamingThinking("");
    }));

    unsubs.push(emitter.on("response_delta", (ev) => {
      const content = (ev.data as Record<string, string>).content;
      setStreamingResponse(prev => prev + content);
    }));

    unsubs.push(emitter.on("response_end", (ev) => {
      const content = (ev.data as Record<string, string>).content;
      if (!content) return;

      setStreamingResponse("");
      setEntries(prev => {
        const last = prev[prev.length - 1];
        if (last && last.type === "assistant" && last.content === content) return prev;
        return [...prev, { id: eid(), type: "assistant" as const, content, status: "done" as const }];
      });
    }));

    unsubs.push(emitter.on("tool_call_start", (ev) => {
      const { name, input: args, id } = ev.data as Record<string, unknown>;
      setCurrentTool(name as string);
      setStreamingResponse("");
      setEntries(prev => [...prev, { 
        id: (id as string) || eid(), 
        type: "tool_call" as const, 
        content: JSON.stringify(args || {}), 
        name: name as string, 
        status: "executing" as const 
      }]);
    }));

    unsubs.push(emitter.on("tool_waiting_approval", (ev) => {
      const { id, name } = ev.data as Record<string, string>;
      setWaitingApproval({ id, name });
      setEntries(prev => prev.map(e => e.id === id ? { ...e, status: "waiting" as const } : e));
    }));

    unsubs.push(emitter.on("tool_call_output", (ev) => {
      const { id, name, output } = ev.data as Record<string, string>;
      if (name === "diff_preview") {
        setEntries(prev => [...prev, { 
          id, 
          type: "diff" as const, 
          content: output, 
          status: "pending" as const,
        }]);
      } else {
        setEntries(prev => [...prev, { 
          id: eid(), 
          type: "tool_output" as const, 
          content: output, 
          name, 
          status: "done" as const 
        }]);
      }
    }));

    unsubs.push(emitter.on("tool_call_end", (ev) => {
      const { id } = ev.data as Record<string, string>;
      setEntries(prev => prev.map(e => e.id === id ? { ...e, status: "done" as const } : e));
    }));

    unsubs.push(emitter.on("error", (ev) => {
      const { message } = ev.data as Record<string, string>;
      setEntries(prev => [...prev, { 
        id: eid(), 
        type: "assistant" as const, 
        content: message, 
        isError: true,
        status: "done" as const 
      }]);
    }));

    unsubs.push(emitter.on("done", () => {
      setIsProcessing(false);
      setCurrentTool("");
      setWaitingApproval(null);
    }));

    return () => {
      for (const u of unsubs) u();
    };
  }, [emitter]);

  useInput((data, key) => {
    if (showPalette) return;

    if (key.ctrl && data === "c") {
      if (processingRef.current) onAbort();
      else { onExit(); exit(); }
      return;
    }

    if (key.escape) {
      setShowPalette(true);
      return;
    }

    if (waitingApproval) {
      const id = approvalRef.current?.id;
      if (!id) return;
      
      if (key.return || data === "y" || data === "Y") {
        handleAccept(id);
        return;
      }
      if (data === "n" || data === "N") {
        handleReject(id);
        return;
      }
      if (data === "a" || data === "A") {
        if (onAcceptAllDiffs) onAcceptAllDiffs();
        handleAccept(id);
        return;
      }
    }
  });

  const handleAccept = (id: string) => {
    onAcceptDiff?.(id);
    setWaitingApproval(null);
    setInput("");
    setEntries(prev => prev.map(e => 
      (e.id === `diff-${id}` && e.type === "diff") ? { ...e, status: "accepted" as const } : e
    ));
  };

  const handleReject = (id: string) => {
    onRejectDiff?.(id);
    setWaitingApproval(null);
    setInput("");
    setEntries(prev => prev.map(e => 
      (e.id === `diff-${id}` && e.type === "diff") ? { ...e, status: "rejected" as const } : e
    ));
  };

  const handleAcceptAll = () => {
    if (waitingApproval) {
      handleAccept(waitingApproval.id);
    }
    if (onAcceptAllDiffs) onAcceptAllDiffs();
  };

  const handleRejectAll = () => {
    if (waitingApproval) {
      handleReject(waitingApproval.id);
    }
    if (onRejectAllDiffs) onRejectAllDiffs();
  };

  const handleCommandSelect = (cmd: string) => {
    setShowPalette(false);
    setInput("");

    if (cmd === "/exit" || cmd === "/quit") {
      onExit();
      exit();
      return;
    }
    if (cmd === "/clear" || cmd === "/new") {
      setEntries([]);
      setWaitingApproval(null);
      return;
    }

    setEntries(prev => [...prev, { id: eid(), type: "user" as const, content: cmd }]);
    setIsProcessing(true);
    onSubmit(cmd);
  };

  const internalSubmit = (val: string) => {
    if (showPalette) return;
    if (!val.trim()) return;
    const clean = val.trim();

    setShowPalette(false);

    if (waitingApproval) {
      const lower = clean.toLowerCase();
      if (lower === "y" || lower === "yes") {
        handleAccept(waitingApproval.id);
        return;
      }
      if (lower === "yy" || lower === "always" || lower === "yes always") {
        handleAccept(waitingApproval.id);
        if (onAcceptAllDiffs) onAcceptAllDiffs();
        return;
      }
      if (lower === "n" || lower === "no") {
        handleReject(waitingApproval.id);
        return;
      }
      if (lower === "nn" || lower === "never" || lower === "no never") {
        handleReject(waitingApproval.id);
        if (onRejectAllDiffs) onRejectAllDiffs();
        onAbort();
        return;
      }
      setInput("");
      return;
    }

    if (clean === "/exit" || clean === "/quit") { onExit(); exit(); return; }
    if (clean === "/clear" || clean === "/new") { setEntries([]); setInput(""); return; }

    setEntries(prev => [...prev, { id: eid(), type: "user" as const, content: clean }]);
    setInput("");
    setIsProcessing(true);
    onSubmit(clean);
  };

  const isSlashMode = input.startsWith("/") && !showPalette;
  const paletteQuery = showPalette ? "" : input.startsWith("/") ? input : "";

  return (
    <Box flexDirection="column" height="100%">
      {/* ── Header ──────────────────────────────────────────────────────── */}
      <Box borderStyle="single" borderBottom={true} borderTop={false} borderLeft={false} borderRight={false} borderColor="gray" paddingX={1}>
        <Text bold color="cyan">rakitkode</Text>
        <Text color="gray">{" │ "}</Text>
        <Text color="white">{modelName}</Text>
        <Text color="gray"> ({providerName})</Text>
        <Box flexGrow={1} />
        {isYolo && (
          <Box>
            <Text color="gray"> </Text>
            <Text bold color="red">YOLO</Text>
          </Box>
        )}
        <Box>
          <Text color="gray"> </Text>
          <Text bold color={mode === "CODE" ? "magenta" : "blue"}>{mode}</Text>
        </Box>
      </Box>

      {/* ── Message List ─────────────────────────────────────────────────── */}
      <Box flexDirection="column" flexGrow={1} overflowY="hidden" paddingX={1}>
        {entries.map((e) => (
          <Box key={e.id} flexDirection="column" marginBottom={0}>
            {e.type === "user" && (
              <Box marginTop={0}>
                <Text color="green" bold>{">"} </Text>
                <Text color="white">{e.content}</Text>
              </Box>
            )}
            
            {e.type === "assistant" && (
              <Box marginTop={0}>
                {e.isError 
                  ? <Text color="red">{e.content}</Text>
                  : <Text color="white">{e.content}</Text>
                }
              </Box>
            )}

            {e.type === "tool_call" && (
              <ToolCallView entry={e} />
            )}

            {e.type === "tool_output" && (
              <Box paddingLeft={2} marginTop={0} marginBottom={0}>
                <Text dimColor>{e.content.length > 500 ? `${e.content.slice(0, 500)}...` : e.content}</Text>
              </Box>
            )}

            {e.type === "diff" && (
              <Box marginTop={0} marginBottom={0}>
                <DiffView content={e.content} status={e.status} />
              </Box>
            )}
          </Box>
        ))}

        {streamingThinking && (
          <Box marginTop={0}>
            <Text color="gray" italic>Thinking: {streamingThinking}</Text>
          </Box>
        )}
        
        {streamingResponse && (
          <Box marginTop={0}>
            <Text color="white">{streamingResponse}</Text>
            <Text color="cyan" bold>{"▊"}</Text>
          </Box>
        )}

        {isProcessing && !streamingResponse && !waitingApproval && (
          <Box marginTop={0}>
            <Text color="cyan" dimColor>...</Text>
          </Box>
        )}
      </Box>

      {/* ── Command Palette (overlay) ───────────────────────────────────── */}
      {showPalette && (
        <Box flexDirection="column" borderStyle="round" borderColor="cyan" paddingX={1}>
          <Box marginBottom={0}>
            <Text bold color="cyan">Commands</Text>
            <Text dimColor>{" (type to filter, ↑↓ navigate, Enter select, Esc close)"}</Text>
          </Box>
          <CommandPalette
            query=""
            onSelect={handleCommandSelect}
            onClose={() => setShowPalette(false)}
            visible={showPalette}
          />
        </Box>
      )}

      {/* ── Slash Autocomplete ─────────────────────────────────────────── */}
      {isSlashMode && (
        <CommandPalette
          query={input}
          onSelect={handleCommandSelect}
          onClose={() => setShowPalette(false)}
          visible={true}
        />
      )}

      {/* ── Approval Banner ─────────────────────────────────────────────── */}
      {waitingApproval && (
        <Box paddingX={1}>
          <ApprovalBanner
            toolName={waitingApproval.name}
            onAccept={() => handleAccept(waitingApproval.id)}
            onReject={() => handleReject(waitingApproval.id)}
            onAcceptAll={handleAcceptAll}
            onRejectAll={handleRejectAll}
          />
        </Box>
      )}

      {/* ── Status Bar ──────────────────────────────────────────────────── */}
      <Box borderStyle="single" borderTop={true} borderBottom={false} borderLeft={false} borderRight={false} borderColor="gray" paddingX={1}>
        <Text color="gray" dimColor>
          {usage?.totalTokens ? `${usage.totalTokens}t` : "0t"}
          {usage?.duration ? ` ${(usage.duration / 1000).toFixed(1)}s` : ""}
        </Text>
      </Box>

      {/* ── Input Area ──────────────────────────────────────────────────── */}
      <Box paddingX={1}>
        <Text bold color="cyan">{">"} </Text>
        {(isProcessing && !waitingApproval) ? (
          <Text dimColor>...</Text>
        ) : (
          <TextInput
            value={input}
            onChange={(val) => {
              if (showPalette) {
                setShowPalette(false);
              }
              setInput(val);
            }}
            onSubmit={internalSubmit}
            placeholder={waitingApproval ? "y/n to approve/reject..." : "Message... (Esc for commands)"}
          />
        )}
      </Box>
    </Box>
  );
};
