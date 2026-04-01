import type { Capability } from "../capability/types.ts";
import { execSync } from "node:child_process";

export class GitDiffTool implements Capability {
  name = "git_diff";
  description = "Get git diff output for staged, unstaged, or all changes.";
  parameters = {
    type: "object",
    properties: {
      target: { type: "string", description: "Git ref to diff against (e.g. HEAD~1)" },
      staged: { type: "boolean", description: "Show only staged changes" },
      path: { type: "string", description: "Specific file path to diff" },
      cwd: { type: "string", description: "Working directory for the git command" },
    },
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const target = (input.target as string) || null;
    const staged = (input.staged as boolean) || false;
    const filePath = (input.path as string) || null;
    const cwd = (input.cwd as string) || process.cwd();

    let cmd = "git diff";
    if (staged) cmd += " --staged";
    if (filePath) cmd += ` -- "${filePath}"`;
    if (target) cmd += ` ${target}`;

    try {
      const output = execSync(cmd, { encoding: "utf-8", maxBuffer: 10 * 1024 * 1024, cwd });
      return { diff: output || "No changes detected." };
    } catch (err: any) {
      if (err.status === 1 && !err.stdout) {
        return { diff: "No changes detected." };
      }
      throw new Error(`Git diff failed: ${err.message}`);
    }
  }
}

export class GitStatusTool implements Capability {
  name = "git_status";
  description = "Get git repository status showing modified, added, deleted, and untracked files.";
  parameters = {
    type: "object",
    properties: {
      cwd: { type: "string", description: "Working directory for the git command" },
    },
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const cwd = (input.cwd as string) || process.cwd();

    try {
      const output = execSync("git status --porcelain=v2", {
        encoding: "utf-8",
        maxBuffer: 10 * 1024 * 1024,
        cwd,
      });

      if (!output.trim()) {
        return { status: "clean", files: [] };
      }

      const files = output
        .trim()
        .split("\n")
        .filter(Boolean)
        .map((line) => {
          const parts = line.split(" ");
          const statusCode = parts[1];
          const filePath = parts.slice(2).join(" ");
          return { status: statusCode, path: filePath };
        });

      return { status: "dirty", files };
    } catch (err: any) {
      throw new Error(`Git status failed: ${err.message}`);
    }
  }
}

export class GitCommitTool implements Capability {
  name = "git_commit";
  description = "Create a git commit with the given message.";
  parameters = {
    type: "object",
    properties: {
      message: { type: "string", description: "Commit message" },
      addAll: { type: "boolean", description: "Stage all changes before committing" },
      cwd: { type: "string", description: "Working directory" },
    },
    required: ["message"],
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const message = input.message as string;
    const addAll = (input.addAll as boolean) || false;
    const cwd = (input.cwd as string) || process.cwd();

    if (!message) throw new Error("message is required");

    if (addAll) {
      execSync("git add -A", { encoding: "utf-8", cwd });
    }

    try {
      const output = execSync(
        `git commit -m ${JSON.stringify(message)}`,
        { encoding: "utf-8", cwd },
      );
      return { success: true, output };
    } catch (err: any) {
      throw new Error(`Git commit failed: ${err.message}`);
    }
  }
}

export class GitCheckoutTool implements Capability {
  name = "git_checkout";
  description = "Checkout a git branch or restore file(s) to their last committed state.";
  parameters = {
    type: "object",
    properties: {
      target: { type: "string", description: "Branch name to checkout" },
      create: { type: "boolean", description: "Create new branch before checkout" },
      paths: { type: "array", items: { type: "string" }, description: "File paths to restore" },
      cwd: { type: "string", description: "Working directory" },
    },
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const target = input.target as string;
    const create = (input.create as boolean) || false;
    const restorePaths = input.paths as string[] | null;
    const cwd = (input.cwd as string) || process.cwd();

    if (restorePaths && restorePaths.length > 0) {
      const pathsStr = restorePaths.map((p) => `"${p}"`).join(" ");
      const output = execSync(`git restore ${pathsStr}`, { encoding: "utf-8", cwd });
      return { success: true, output };
    }

    if (!target) throw new Error("target is required");

    const cmd = create ? `git checkout -b ${target}` : `git checkout ${target}`;
    const output = execSync(cmd, { encoding: "utf-8", cwd });
    return { success: true, output };
  }
}

export class GitLogTool implements Capability {
  name = "git_log";
  description = "Get recent git commit history.";
  parameters = {
    type: "object",
    properties: {
      count: { type: "number", description: "Number of commits to show (default: 10)" },
      cwd: { type: "string", description: "Working directory" },
    },
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const count = (input.count as number) || 10;
    const cwd = (input.cwd as string) || process.cwd();

    try {
      const output = execSync(
        `git log --oneline -n ${count}`,
        { encoding: "utf-8", cwd },
      );
      const commits = output.trim().split("\n").filter(Boolean);
      return { commits };
    } catch (err: any) {
      throw new Error(`Git log failed: ${err.message}`);
    }
  }
}
