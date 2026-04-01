import type { FileChange } from "../../types.ts";
import { execSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";

export interface ContextResult {
  task: string;
  plan: string;
  criticalFiles: string[];
  relatedFunctions: string[];
  summaries: string[];
  errors: string[];
  fileChanges: FileChange[];
}

export class ContextEngine {
  async buildContext(
    task: string,
    plan: string,
    workDir: string,
  ): Promise<ContextResult> {
    const criticalFiles = await this.findCriticalFiles(task, workDir);
    const relatedFunctions = await this.findRelatedSymbols(task, workDir);
    const summaries = criticalFiles.length > 0
      ? await this.summarizeFiles(criticalFiles, workDir)
      : [];

    return {
      task,
      plan,
      criticalFiles,
      relatedFunctions,
      summaries,
      errors: [],
      fileChanges: this.detectFileChanges(workDir),
    };
  }

  private async findCriticalFiles(task: string, workDir: string): Promise<string[]> {
    const keywords = this.extractKeywords(task);
    const files = new Set<string>();

    for (const keyword of keywords) {
      try {
        const result = execSync(
          `rg -l --max-count 5 "${keyword}" "${workDir}" 2>/dev/null || true`,
          { encoding: "utf-8" },
        );
        for (const file of result.trim().split("\n").filter(Boolean)) {
          if (!file.includes("node_modules") && !file.includes(".git")) {
            files.add(file);
          }
        }
      } catch {
        // skip
      }
    }

    return [...files].slice(0, 20);
  }

  private async findRelatedSymbols(task: string, workDir: string): Promise<string[]> {
    const keywords = this.extractKeywords(task);
    const symbols = new Set<string>();

    for (const keyword of keywords) {
      try {
        const result = execSync(
          `rg --no-filename "^(export )?(function|class|interface|type|const|let|var)\\s+${keyword}" "${workDir}" -m 5 2>/dev/null || true`,
          { encoding: "utf-8" },
        );
        for (const line of result.trim().split("\n").filter(Boolean)) {
          symbols.add(line.trim());
        }
      } catch {
        // skip
      }
    }

    return [...symbols].slice(0, 30);
  }

  private async summarizeFiles(files: string[], workDir: string): Promise<string[]> {
    return files.map((file) => {
      const relativePath = file.replace(`${workDir}/`, "");
      try {
        const content = execSync(`wc -l "${file}" 2>/dev/null`, {
          encoding: "utf-8",
        }).trim();
        return `${relativePath}: ${content}`;
      } catch {
        return `${relativePath}: (could not read)`;
      }
    });
  }

  private extractKeywords(text: string): string[] {
    const stopWords = new Set([
      "the", "a", "an", "is", "are", "was", "were", "be", "been", "being",
      "have", "has", "had", "do", "does", "did", "will", "would", "could",
      "should", "may", "might", "must", "can", "to", "of", "in", "for",
      "on", "with", "at", "by", "from", "as", "into", "through", "during",
      "before", "after", "above", "below", "between", "out", "off", "over",
      "under", "again", "further", "then", "once", "and", "but", "or",
      "nor", "not", "so", "yet", "both", "either", "neither", "each",
      "every", "all", "any", "few", "more", "most", "other", "some",
      "such", "no", "only", "own", "same", "than", "too", "very", "just",
      "because", "if", "when", "where", "how", "what", "which", "who",
      "whom", "this", "that", "these", "those", "it", "its", "me", "my",
      "we", "our", "you", "your", "he", "she", "they", "them", "their",
      "make", "add", "create", "implement", "fix", "update", "please",
    ]);

    return text
      .toLowerCase()
      .replace(/[^a-z0-9\s_]/g, " ")
      .split(/\s+/)
      .filter((w) => w.length > 2 && !stopWords.has(w))
      .slice(0, 10);
  }

  private detectFileChanges(workDir: string): FileChange[] {
    const changes: FileChange[] = [];

    try {
      const status = execSync("git status --porcelain=v2", {
        encoding: "utf-8",
        cwd: workDir,
      });

      for (const line of status.trim().split("\n").filter(Boolean)) {
        const parts = line.split(" ");
        const statusCode = parts[1];
        const filePath = parts.slice(2).join(" ");

        let type: FileChange["type"] = "modified";
        if (statusCode.includes("?")) type = "created";
        else if (statusCode.includes("D")) type = "deleted";

        changes.push({ path: filePath, type });
      }
    } catch {
      // not a git repo
    }

    return changes;
  }
}
