import type { Capability } from "../capability/types.ts";
import { resolve } from "node:path";

export class GrepTool implements Capability {
  name = "grep";
  description = "Search for regex patterns in files using ripgrep. Returns matching lines with file paths and line numbers.";
  parameters = {
    type: "object",
    properties: {
      pattern: { type: "string", description: "Regex pattern to search for" },
      path: { type: "string", description: "Directory to search in (default: current directory)" },
      include: { type: "string", description: "File glob filter, e.g. '*.ts'" },
      maxResults: { type: "number", description: "Maximum number of matches (default: 50)" },
    },
    required: ["pattern"],
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const pattern = input.pattern as string;
    const path = (input.path as string) || ".";
    const include = (input.include as string) || null;
    const maxResults = (input.maxResults as number) || 50;

    if (!pattern) throw new Error("pattern is required");

    const searchPath = resolve(path);

    const args = ["rg", "--json", "--smart-case", "--hidden", "--glob", "!.git/*", "--glob", "!node_modules/*", "--glob", "!.rakitkode/*", pattern, searchPath];
    if (include) {
      args.push("--glob", include);
    }
    args.push("-m", maxResults.toString());

    try {
      const proc = Bun.spawnSync(args, { stdout: "pipe" });
      const result = proc.stdout?.toString() || "";
      const matches = result
        .trim()
        .split("\n")
        .filter(Boolean)
        .map((line) => {
          try {
            return JSON.parse(line);
          } catch {
            return null;
          }
        })
        .filter(Boolean);

      return { matches, count: matches.length };
    } catch (err: any) {
      return { matches: [], count: 0, error: err.message };
    }
  }
}

export class SymbolSearchTool implements Capability {
  name = "symbol_search";
  description = "Search for symbol definitions (functions, classes, variables) in code files.";
  parameters = {
    type: "object",
    properties: {
      query: { type: "string", description: "Symbol name to search for" },
      path: { type: "string", description: "Directory to search in" },
      kind: { type: "string", description: "Symbol kind: 'function', 'class', or 'variable'" },
    },
    required: ["query"],
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const query = input.query as string;
    const path = (input.path as string) || ".";
    const kind = (input.kind as string) || null;

    if (!query) throw new Error("query is required");

    const searchPath = resolve(path);

    let pattern = query;
    if (kind === "function") {
      pattern = `(function|async function|const|let|var)\\s+${query}`;
    } else if (kind === "class") {
      pattern = `(class|interface|type)\\s+${query}`;
    } else if (kind === "variable") {
      pattern = `(const|let|var)\\s+${query}`;
    }

    const args = ["rg", "--json", pattern, searchPath, "-m", "30"];
    if (kind) {
      const exts = kind === "function" || kind === "class" || kind === "variable"
        ? "*.{ts,tsx,js,jsx,go,py,rs}"
        : null;
      if (exts) {
          args.push("--glob", exts);
      }
    }

    try {
      const proc = Bun.spawnSync(args, { stdout: "pipe" });
      const result = proc.stdout?.toString() || "";
      const matches = result
        .trim()
        .split("\n")
        .filter(Boolean)
        .map((line) => {
          try {
            return JSON.parse(line);
          } catch {
            return null;
          }
        })
        .filter(Boolean);

      return { symbols: matches, count: matches.length };
    } catch (err: any) {
      return { symbols: [], count: 0, error: err.message };
    }
  }
}
