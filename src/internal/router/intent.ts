import type { Intent } from "../../types.ts";
import type { LLMProvider, LLMMessage } from "../llm/provider.ts";

export interface IntentResult {
  intent: Intent;
  confidence: number;
  reasoning: string;
}

const INTENT_KEYWORDS: Record<Intent, string[]> = {
  code: [
    "write", "create", "implement", "add", "refactor", "fix", "modify",
    "change", "update", "edit", "remove", "delete", "rename", "move",
    "extract", "implement", "build", "code", "function", "class",
    "component", "module", "api", "endpoint", "handler",
  ],
  plan: [
    "plan", "design", "architect", "strategy", "approach", "how should",
    "what's the best way", "breakdown", "roadmap", "structure",
    "organize", "layout", "steps",
  ],
  debug: [
    "debug", "error", "fix error", "bug", "issue", "broken", "failing",
    "crash", "exception", "not working", "wrong", "unexpected",
    "test failure", "failing test", "trace", "investigate",
  ],
  chat: [
    "explain", "what is", "what does", "how does", "why", "tell me",
    "describe", "help me understand", "what's the difference",
    "compare", "summarize", "hello", "hi", "hey", "halo", "help",
  ],
};

function classifyByKeywords(input: string): IntentResult {
  const lower = input.toLowerCase();
  const scores: Record<Intent, number> = { code: 0, plan: 0, debug: 0, chat: 0 };

  for (const [intent, keywords] of Object.entries(INTENT_KEYWORDS)) {
    for (const keyword of keywords) {
      if (lower.includes(keyword)) {
        scores[intent as Intent] += 1;
      }
    }
  }

  let bestIntent: Intent = "chat";
  let bestScore = 0;
  for (const [intent, score] of Object.entries(scores)) {
    if (score > bestScore) {
      bestScore = score;
      bestIntent = intent as Intent;
    }
  }

  return {
    intent: bestIntent,
    confidence: bestScore > 0 ? Math.min(bestScore / 5, 1) : 0.3,
    reasoning: bestScore > 0
      ? `Matched ${bestScore} keyword(s) for ${bestIntent}`
      : "No strong keyword match, defaulting to chat",
  };
}

export class IntentRouter {
  private llm: LLMProvider | null = null;
  private useLLM = false;

  setLLM(llm: LLMProvider): void {
    this.llm = llm;
    this.useLLM = true;
  }

  async detect(input: string): Promise<IntentResult> {
    if (input.startsWith("/")) {
      return {
        intent: this.commandToIntent(input),
        confidence: 1,
        reasoning: "Explicit command",
      };
    }

    const keywordResult = classifyByKeywords(input);

    if (keywordResult.confidence >= 0.6 || !this.useLLM || !this.llm) {
      return keywordResult;
    }

    try {
      const messages: LLMMessage[] = [
        {
          role: "system",
          content: `Classify this user input into exactly one intent: "code", "plan", "debug", or "chat".
Respond with JSON: {"intent": "...", "confidence": 0.0-1.0, "reasoning": "..."}

- code: writing, editing, or modifying code
- plan: designing architecture, planning approach
- debug: fixing errors, debugging failures
- chat: asking questions, explaining concepts`,
        },
        { role: "user", content: input },
      ];

      const response = await this.llm.chat(messages);
      const parsed = JSON.parse(response.content) as IntentResult;
      return parsed;
    } catch {
      return keywordResult;
    }
  }

  private commandToIntent(input: string): Intent {
    const cmd = input.split(" ")[0].toLowerCase();
    const codeCommands = ["/run", "/review", "/diff", "/accept", "/reject", "/accept-all", "/files"];
    const planCommands = ["/agents", "/tools", "/models", "/mcp"];
    const debugCommands = ["/debug", "/logs"];

    if (codeCommands.includes(cmd)) return "code";
    if (planCommands.includes(cmd)) return "plan";
    if (debugCommands.includes(cmd)) return "debug";
    return "chat";
  }
}
