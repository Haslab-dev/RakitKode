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

// ── Components ─────────────────────────────────────────────────────────────

const Spinner: React.FC = () => {
  const [frame, setFrame] = useState(0);
  const frames = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

  useEffect(() => {
    const timer = setInterval(() => {
      setFrame((f) => (f + 1) % frames.length);
    }, 80);
    return () => clearInterval(timer);
  }, []);

  return <Text color="cyan">{frames[frame]}</Text>;
};

const Header: React.FC<{
  model: string;
  provider: string;
  mode: string;
  isYolo: boolean;
  status: string;
}> = ({ model, provider, mode, isYolo, status }) => (
  <Box flexDirection="column" marginBottom={1}>
    <Box paddingX={1} justifyContent="space-between">
      <Box>
        <Text bold color="blue"> RAKITKODE </Text>
        <Text color="gray">v0.2.0</Text>
      </Box>
      <Box>
        <Text color="cyan" bold>{status.toUpperCase()}</Text>
      </Box>
    </Box>
    <Box paddingX={1} borderStyle="single" borderTop={false} borderLeft={false} borderRight={false} borderColor="gray">
      <Text dimColor>Model: </Text>
      <Text color="cyan" bold>{model}</Text>
      <Text dimColor> ({provider})</Text>
      <Box flexGrow={1} />
      <Box>
        <Text dimColor>Mode: </Text>
        <Text color={mode === "CODE" ? "magenta" : "blue"} bold>{mode}</Text>
        {isYolo && (
          <>
            <Text color="gray"> │ </Text>
            <Text color="red" bold>YOLO ⚡</Text>
          </>
        )}
      </Box>
    </Box>
  </Box>
);

const StatusBar: React.FC<{
  usage: TokenUsageData | null;
  isProcessing: boolean;
}> = ({ usage, isProcessing }) => {
  return (
    <Box paddingX={1} borderStyle="single" borderBottom={false} borderLeft={false} borderRight={false} borderColor="gray" justifyContent="space-between">
      <Box>
        {isProcessing ? (
          <Box>
            <Spinner />
            <Text> Thinking...</Text>
          </Box>
        ) : (
          <Text color="green">● Ready</Text>
        )}
      </Box>
      <Box>
        <Text dimColor>Tokens: </Text>
        <Text color="white">
          {usage ? `${usage.promptTokens}↑ / ${usage.completionTokens}↓` : "0↑ / 0↓"}
        </Text>
        <Text color="gray"> │ </Text>
        <Text dimColor>Time: </Text>
        <Text color="white">
          {usage?.duration ? `${(usage.duration / 1000).toFixed(1)}s` : "0.0s"}
        </Text>
      </Box>
    </Box>
  );
};

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
    <Box flexDirection="column" borderStyle="round" borderColor="cyan" paddingX={1} marginY={1}>
      <Box marginBottom={1}>
        <Text bold color="cyan">Command Palette</Text>
      </Box>
      {filtered.slice(0, 10).map((c, i) => (
        <Box key={c.cmd}>
          <Text color={i === selectedIndex ? "cyan" : "gray"}>
            {i === selectedIndex ? "❯ " : "  "}
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
    <Box flexDirection="column" borderStyle="single" borderColor={borderColor} paddingX={1} marginY={1}>
      <Box marginBottom={1}>
        <Text bold color={borderColor}>PATCH {status?.toUpperCase() || "PENDING"}</Text>
      </Box>
      {lines.map((line, i) => {
        if (line.startsWith("+++") || line.startsWith("---")) return <Text key={i} color="cyan">{line}</Text>;
        if (line.startsWith("@@")) return <Text key={i} color="magenta" dimColor>{line}</Text>;
        if (line.startsWith("+")) return <Text key={i} color="green">{line}</Text>;
        if (line.startsWith("-")) return <Text key={i} color="red">{line}</Text>;
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
  const icon = isWaiting ? "⏳" : isActive ? "⚙ " : "✔ ";

  const argsStr = entry.content;
  const truncated = argsStr.length > 100 ? `${argsStr.slice(0, 100)}...` : argsStr;

  return (
    <Box paddingLeft={2} marginY={0}>
      <Text color={color} bold>{icon}{entry.name}</Text>
      <Text dimColor> {truncated}</Text>
    </Box>
  );
};

// ── Approval Banner ────────────────────────────────────────────────────────

const ApprovalBanner: React.FC<{
  onAccept: () => void;
  onReject: () => void;
  toolName: string;
}> = ({ onAccept, onReject, toolName }) => (
  <Box borderStyle="round" borderColor="yellow" paddingX={1} marginY={1} flexDirection="column">
    <Text bold color="yellow">Pending Approval: {toolName}</Text>
    <Box marginTop={1}>
      <Text>Allow this action? </Text>
      <Text bold color="green">[y]es</Text>
      <Text> / </Text>
      <Text bold color="red">[n]o</Text>
      <Text dimColor> (Always/Never also available in menu)</Text>
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
      setEntries(prev => [...prev, { id: eid(), type: "assistant" as const, content, status: "done" as const }]);
    }));

    unsubs.push(emitter.on("tool_call_start", (ev) => {
      const { name, input: args, id } = ev.data as Record<string, unknown>;
      setCurrentTool(name as string);
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
        setEntries(prev => [...prev, { id: `diff-${id}`, type: "diff" as const, content: output, status: "pending" as const }]);
      } else {
        setEntries(prev => [...prev, { id: eid(), type: "tool_output" as const, content: output, name, status: "done" as const }]);
      }
    }));

    unsubs.push(emitter.on("tool_call_end", (ev) => {
      const { id } = ev.data as Record<string, string>;
      setEntries(prev => prev.map(e => e.id === id ? { ...e, status: "done" as const } : e));
    }));

    unsubs.push(emitter.on("error", (ev) => {
      const { message } = ev.data as Record<string, string>;
      setEntries(prev => [...prev, { id: eid(), type: "assistant" as const, content: message, isError: true, status: "done" as const }]);
    }));

    unsubs.push(emitter.on("done", () => {
      setIsProcessing(false);
      setCurrentTool("");
      setWaitingApproval(null);
    }));

    return () => u();
    function u() { for (const unsub of unsubs) unsub(); }
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
    }
  });

  const handleAccept = (id: string) => {
    onAcceptDiff?.(id);
    setWaitingApproval(null);
    setInput("");
    setEntries(prev => [
      ...prev.map(e => (e.id === `diff-${id}`) ? { ...e, status: "accepted" as const } : e),
      { id: eid(), type: "assistant" as const, content: "✓ Changes accepted and applied.", status: "done" as const }
    ]);
  };

  const handleReject = (id: string) => {
    onRejectDiff?.(id);
    setWaitingApproval(null);
    setInput("");
    setEntries(prev => [
      ...prev.map(e => (e.id === `diff-${id}`) ? { ...e, status: "rejected" as const } : e),
      { id: eid(), type: "assistant" as const, content: "✗ Changes rejected.", status: "done" as const }
    ]);
  };

  const handleCommandSelect = (cmd: string) => {
    setShowPalette(false);
    setInput("");
    if (cmd === "/exit") { onExit(); exit(); return; }
    if (cmd === "/clear") { setEntries([]); return; }
    setEntries(prev => [...prev, { id: eid(), type: "user" as const, content: cmd }]);
    setIsProcessing(true);
    onSubmit(cmd);
  };

  return (
    <Box flexDirection="column" height="100%">
      <Header 
        model={modelName} 
        provider={providerName} 
        mode={mode} 
        isYolo={isYolo} 
        status={isProcessing ? "Processing" : waitingApproval ? "Approval Needed" : "Idle"}
      />

      <Box flexDirection="column" flexGrow={1} overflowY="hidden" paddingX={1}>
        {entries.map((e) => (
          <Box key={e.id} flexDirection="column" marginBottom={1}>
            {e.type === "user" && (
              <Box>
                <Text color="cyan" bold>❯ </Text>
                <Text color="white">{e.content}</Text>
              </Box>
            )}
            {e.type === "assistant" && (
              <Box paddingLeft={1} borderStyle="single" borderLeft={true} borderTop={false} borderRight={false} borderBottom={false} borderColor="gray">
                <Text color={e.isError ? "red" : "white"}>{e.content}</Text>
              </Box>
            )}
            {e.type === "tool_call" && <ToolCallView entry={e} />}
            {e.type === "diff" && <DiffView content={e.content} status={e.status} />}
          </Box>
        ))}

        {streamingThinking && (
          <Box marginY={1} paddingX={1} borderStyle="round" borderColor="gray">
            <Text italic dimColor>Thinking: {streamingThinking}</Text>
          </Box>
        )}
        
        {streamingResponse && (
          <Box paddingLeft={1} borderStyle="single" borderLeft={true} borderTop={false} borderRight={false} borderBottom={false} borderColor="cyan">
            <Text color="white">{streamingResponse}</Text>
            <Text color="cyan">▊</Text>
          </Box>
        )}
      </Box>

      {(showPalette || (input.startsWith("/") && input.length > 0)) && (
        <Box paddingX={1}>
          <CommandPalette
            query={input}
            onSelect={handleCommandSelect}
            onClose={() => setShowPalette(false)}
            visible={true}
          />
        </Box>
      )}

      {waitingApproval && (
        <Box paddingX={1}>
          <ApprovalBanner
            toolName={waitingApproval.name}
            onAccept={() => handleAccept(waitingApproval.id)}
            onReject={() => handleReject(waitingApproval.id)}
          />
        </Box>
      )}

      <StatusBar usage={usage} isProcessing={isProcessing} />

      <Box paddingX={1} marginBottom={1}>
        <Text bold color="cyan">❯ </Text>
        {waitingApproval ? (
          <Text dimColor>Please press y/n to approve or reject the changes...</Text>
        ) : (
          <TextInput
            value={input}
            onChange={setInput}
            onSubmit={(val) => {
              if (!val.trim()) return;
              if (val.startsWith("/") && !showPalette) {
                const matched = COMMANDS.find((c) => c.cmd === val.trim());
                if (matched) {
                  handleCommandSelect(matched.cmd);
                  return;
                }
              }
              setEntries(prev => [...prev, { id: eid(), type: "user" as const, content: val }]);
              setInput("");
              setIsProcessing(true);
              onSubmit(val);
            }}
            placeholder="Describe what you want to build..."
            showCursor={!isProcessing}
          />
        )}
      </Box>
    </Box>
  );
};

