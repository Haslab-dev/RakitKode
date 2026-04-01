import { existsSync, statSync } from "node:fs";
import { resolve, basename, relative, dirname, join } from "node:path";
import { execSync } from "node:child_process";

export interface FileEntity {
  name: string;
  path: string;
  lastSeen: Date;
  type: "file" | "directory";
}

export interface ToolAction {
  tool: string;
  args: Record<string, unknown>;
  result: string;
  timestamp: Date;
  success: boolean;
}

export interface WorkingMemory {
  cwd: string;
  files: FileEntity[];
  actions: ToolAction[];
  projectRoot: string;
}

function isSubPath(parent: string, child: string): boolean {
  const rel = relative(parent, child);
  return !rel.startsWith("..") && !rel.startsWith("/");
}

export class MemoryStore {
  private memory: WorkingMemory;

  constructor(cwd: string) {
    this.memory = {
      cwd,
      files: [],
      actions: [],
      projectRoot: this.detectProjectRoot(cwd),
    };
  }

  private detectProjectRoot(startDir: string): string {
    let dir = resolve(startDir);
    const markers = ["package.json", "go.mod", "Cargo.toml", "pyproject.toml", ".git"];

    for (let i = 0; i < 30; i++) {
      for (const m of markers) {
        if (existsSync(join(dir, m))) return dir;
      }
      const parent = dirname(dir);
      if (parent === dir) break;
      dir = parent;
    }
    return startDir;
  }

  getCwd(): string {
    return this.memory.cwd;
  }

  getProjectRoot(): string {
    return this.memory.projectRoot;
  }

  recordFile(path: string, type: "file" | "directory" = "file"): void {
    const absPath = resolve(path);
    const existing = this.memory.files.findIndex((f) => f.path === absPath);
    if (existing >= 0) {
      this.memory.files[existing].lastSeen = new Date();
      this.memory.files[existing].type = type;
    } else {
      this.memory.files.push({
        name: basename(absPath),
        path: absPath,
        lastSeen: new Date(),
        type,
      });
    }
    if (this.memory.files.length > 200) {
      this.memory.files = this.memory.files
        .sort((a, b) => b.lastSeen.getTime() - a.lastSeen.getTime())
        .slice(0, 100);
    }
  }

  recordAction(tool: string, args: Record<string, unknown>, result: string, success: boolean): void {
    this.memory.actions.push({
      tool,
      args: { ...args },
      result: result.substring(0, 500),
      timestamp: new Date(),
      success,
    });
    if (this.memory.actions.length > 100) {
      this.memory.actions = this.memory.actions.slice(-50);
    }
  }

  resolveFile(input: string): { path: string; found: boolean; method: string } {
    const trimmed = input.trim().replace(/^@/, "").replace(/^\[/, "").replace(/\]$/, "");

    if (!trimmed) return { path: trimmed, found: false, method: "none" };

    const absPath = resolve(trimmed);
    if (existsSync(absPath)) {
      this.recordFile(absPath);
      return { path: absPath, found: true, method: "exact" };
    }

    const relToCwd = resolve(this.memory.cwd, trimmed);
    if (existsSync(relToCwd)) {
      this.recordFile(relToCwd);
      return { path: relToCwd, found: true, method: "relative" };
    }

    const relToRoot = resolve(this.memory.projectRoot, trimmed);
    if (existsSync(relToRoot)) {
      this.recordFile(relToRoot);
      return { path: relToRoot, found: true, method: "project-root" };
    }

    const recentFiles = this.memory.files
      .filter((f) => f.type === "file")
      .sort((a, b) => b.lastSeen.getTime() - a.lastSeen.getTime());

    const exactNameMatch = recentFiles.find((f) => f.name === trimmed);
    if (exactNameMatch) {
      this.recordFile(exactNameMatch.path);
      return { path: exactNameMatch.path, found: true, method: "entity-name" };
    }

    if (trimmed.includes("/") || trimmed.includes("\\")) {
      const parts = trimmed.replace(/\\/g, "/").split("/");
      const fileName = parts[parts.length - 1];
      const dirHint = parts.slice(0, -1).join("/");

      const matches = recentFiles.filter(
        (f) => f.name === fileName && (!dirHint || f.path.includes(dirHint)),
      );
      if (matches.length === 1) {
        this.recordFile(matches[0].path);
        return { path: matches[0].path, found: true, method: "fuzzy-name" };
      }
      if (matches.length > 1) {
        const best = matches[0];
        this.recordFile(best.path);
        return { path: best.path, found: true, method: "fuzzy-name-multi" };
      }
    }

    const partialMatch = recentFiles.find(
      (f) =>
        f.name === trimmed ||
        f.name.startsWith(trimmed) ||
        trimmed.startsWith(f.name) ||
        f.name.includes(trimmed),
    );
    if (partialMatch) {
      this.recordFile(partialMatch.path);
      return { path: partialMatch.path, found: true, method: "partial-match" };
    }

    try {
      // Add glob search for better discovery if regular methods fail
      const globResult = execSync(
        `find ${this.memory.projectRoot} -name "*${trimmed}*" -type f -not -path '*/.*' -not -path '*/node_modules/*' | head -3`,
        { encoding: "utf-8", timeout: 2000 },
      ).trim();
      if (globResult) {
        const found = globResult.split("\n")[0];
        if (found && existsSync(found)) {
          this.recordFile(found);
          return { path: found, found: true, method: "find" };
        }
      }
    } catch {
      // ignore
    }

    return { path: absPath, found: false, method: "none" };
  }

  getRelativePath(absPath: string): string {
    const root = this.memory.projectRoot;
    return absPath.startsWith(root)
      ? absPath.substring(root.length).replace(/^\//, "")
      : absPath;
  }

  getContextForLLM(): string {
    const lines: string[] = [];
    lines.push(`Working directory: ${this.memory.cwd}`);
    lines.push(`Project root: ${this.memory.projectRoot}`);

    const recent = this.memory.files
      .filter((f) => f.type === "file")
      .sort((a, b) => b.lastSeen.getTime() - a.lastSeen.getTime())
      .slice(0, 20);

    if (recent.length > 0) {
      lines.push("Recently accessed files:");
      for (const f of recent) {
        const rel = relative(this.memory.cwd, f.path);
        lines.push(`  - ${rel}`);
      }
    }

    const recentActions = this.memory.actions.slice(-10);
    if (recentActions.length > 0) {
      lines.push("Recent actions:");
      for (const a of recentActions) {
        const status = a.success ? "ok" : "fail";
        const detail = Object.entries(a.args)
          .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
          .join(" ");
        lines.push(`  - ${a.tool}(${detail.substring(0, 80)}) [${status}]`);
      }
    }

    return lines.join("\n");
  }

  getFileChanges(): { created: string[]; modified: string[]; deleted: string[] } {
    const created: string[] = [];
    const modified: string[] = [];

    for (const action of this.memory.actions) {
      if (action.tool === "write_file" && action.success) {
        const p = (action.args.path as string) || "";
        created.push(relative(this.memory.cwd, resolve(p)));
      }
      if (action.tool === "apply_patch" && action.success) {
        const p = (action.args.diff as string) || "";
        const match = p.match(/^\+\+\+ [ab]\/(.+)$/m);
        if (match) {
          const filePath = match[1];
          if (!created.includes(filePath)) {
            modified.push(relative(this.memory.cwd, resolve(filePath)));
          }
        }
      }
      if (action.tool === "read_file" && action.success) {
        const p = (action.args.path as string) || "";
        modified.push(relative(this.memory.cwd, resolve(p)));
      }
    }

    const createdUnique = [...new Set(created)];
    const modifiedUnique = [...new Set(modified)].filter((m) => !createdUnique.includes(m));

    return { created: createdUnique, modified: modifiedUnique, deleted: [] };
  }

  getRecentFiles(): FileEntity[] {
    return this.memory.files
      .filter((f) => f.type === "file")
      .sort((a, b) => b.lastSeen.getTime() - a.lastSeen.getTime())
      .slice(0, 20);
  }

  getMemory(): WorkingMemory {
    return { ...this.memory, files: [...this.memory.files], actions: [...this.memory.actions] };
  }
}
