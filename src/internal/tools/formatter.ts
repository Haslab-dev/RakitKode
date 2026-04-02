import type { MemoryStore } from "../context/memory.ts";
import { resolve, relative } from "node:path";

export interface FormattedOutput {
  display: string;
  raw: string;
  success: boolean;
  toolName: string;
}

export function formatToolOutput(
  tool: string,
  args: Record<string, unknown>,
  result: Record<string, unknown> | string | undefined,
  memory?: MemoryStore,
  cwd?: string,
): FormattedOutput {
  const raw = typeof result === "string" ? result : JSON.stringify(result, null, 2);
  const success = !raw.includes('"error"') && !raw.startsWith("Error:");
  const base = cwd || process.cwd();

  let display = "";

  switch (tool) {
    case "read_file": {
      const path = (args.path as string) || "";
      const rel = relativeSafe(base, resolve(path));
      const err = (result as any)?.error;
      if (err) {
        display = `Error reading ${rel}: ${err}`;
      } else {
        const content = (result as any)?.content as string || "";
        const lines = content.split("\n");
        display = `${rel} (${lines.length} lines)`;
      }
      break;
    }

    case "write_file": {
      const path = (args.path as string) || "";
      const rel = relativeSafe(base, resolve(path));
      const ok = (result as any)?.success;
      if (ok) {
        display = `Created: ${rel}`;
        memory?.recordFile(resolve(path));
      } else {
        display = `Failed to create: ${rel}`;
      }
      break;
    }

    case "apply_patch": {
      const diff = (args.diff as string) || "";
      const files = extractFilesFromDiff(diff);
      const ok = (result as any)?.success;
      if (ok) {
        const count = (result as any)?.appliedCount ?? files.length;
        display = `Patched ${count} file(s): ${files.join(", ")}`;
        for (const f of files) {
          memory?.recordFile(resolve(f), "file");
        }
      } else {
        display = `Patch failed: ${files.join(", ")}`;
      }
      break;
    }

    case "list_files": {
      const path = (args.path as string) || ".";
      const rel = relativeSafe(base, resolve(path));
      const files = (result as any)?.files as any[] || [];
      const dirs = files.filter((f: any) => f.isDir);
      const regular = files.filter((f: any) => !f.isDir);
      for (const f of regular) {
        memory?.recordFile(resolve(f.path));
      }
      const parts: string[] = [];
      if (dirs.length > 0) parts.push(`${dirs.map((d: any) => d.name + "/").join("  ")}`);
      if (regular.length > 0) parts.push(regular.map((f: any) => f.name).join("  "));
      display = parts.length > 0 ? parts.join("\n") : "(empty)";
      break;
    }

    case "delete_file": {
      const path = (args.path as string) || "";
      const rel = relativeSafe(base, resolve(path));
      const ok = (result as any)?.success;
      display = ok ? `Deleted: ${rel}` : `Failed to delete: ${rel}`;
      break;
    }

    case "run_command": {
      const cmd = (args.command as string) || "";
      const exitCode = (result as any)?.exitCode ?? -1;
      const ok = exitCode === 0;
      const stdout = ((result as any)?.stdout || "").trim();
      const stderr = ((result as any)?.stderr || "").trim();
      display = `$ ${cmd}`;
      if (ok && stdout) {
        const lines = stdout.split("\n");
        display += "\n" + (lines.length > 15
          ? lines.slice(0, 15).join("\n") + `\n... (${lines.length - 15} more lines)`
          : stdout);
      }
      if (!ok && stderr) {
        const lines = stderr.split("\n");
        display += `\nError (exit ${exitCode}):\n` + (lines.length > 10
          ? lines.slice(0, 10).join("\n") + `\n... (${lines.length - 10} more lines)`
          : stderr);
      }
      display += `\nExit code: ${exitCode}`;
      break;
    }

    case "run_tests": {
      const exitCode = (result as any)?.exitCode ?? -1;
      const ok = exitCode === 0;
      const stdout = ((result as any)?.stdout || "").trim();
      display = ok ? "Tests passed" : `Tests failed (exit ${exitCode})`;
      if (stdout) {
        const lines = stdout.split("\n");
        display += "\n" + (lines.length > 15
          ? lines.slice(0, 15).join("\n") + `\n...`
          : stdout);
      }
      break;
    }

    case "grep":
    case "symbol_search": {
      const pattern = (args.pattern as string) || (args.query as string) || "";
      const matches = (result as any)?.matches || (result as any)?.symbols || [];
      display = `${matches.length} result(s) for "${pattern}"`;
      break;
    }

    case "git_diff": {
      const diff = (result as any)?.diff as string || "";
      if (diff === "No changes detected.") {
        display = "No changes.";
      } else {
        const files = extractFilesFromDiff(diff);
        display = `Changed ${files.length} file(s): ${files.join(", ")}`;
      }
      break;
    }

    case "git_status": {
      const status = (result as any)?.status as string;
      const files = (result as any)?.files as any[] || [];
      if (status === "clean") {
        display = "Working tree clean.";
      } else {
        display = `${files.length} changed file(s)`;
      }
      break;
    }

    case "git_commit": {
      const ok = (result as any)?.success;
      display = ok ? "Committed." : "Commit failed.";
      break;
    }

    case "git_checkout": {
      const ok = (result as any)?.success;
      const target = (args.target as string) || "";
      display = ok ? `Switched to ${target || "branch"}` : "Checkout failed.";
      break;
    }

    case "git_log": {
      const commits = (result as any)?.commits as string[] || [];
      display = commits.slice(0, 5).join("\n");
      if (commits.length > 5) display += `\n... and ${commits.length - 5} more`;
      break;
    }

    default:
      display = raw.substring(0, 300);
  }

  return { display: display || "(no output)", raw, success, toolName: tool };
}

function relativeSafe(from: string, to: string): string {
  try {
    return relative(from, to);
  } catch {
    return to;
  }
}

function extractFilesFromDiff(diff: string): string[] {
  const files: string[] = [];
  for (const line of diff.split("\n")) {
    const match = line.match(/^\+\+\+ [ab]\/(.+)$/);
    if (match && !files.includes(match[1])) {
      files.push(match[1]);
    }
  }
  return files;
}
