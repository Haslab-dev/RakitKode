import type { Hunk, HunkLine, Patch } from "../../types.ts";

export class PatchEngine {
  parseUnifiedDiff(diffText: string): Array<{
    filePath: string;
    hunks: Hunk[];
  }> {
    const results: Array<{ filePath: string; hunks: Hunk[] }> = [];
    const fileBlocks = diffText.split(/^diff --git /m).filter(Boolean);

    for (const block of fileBlocks) {
      const lines = block.split("\n");
      let filePath = "";
      const hunks: Hunk[] = [];
      let currentHunk: Hunk | null = null;

      for (const line of lines) {
        const fileMatch = line.match(/^--- [ab]\/(.+)$/);
        const fileMatch2 = line.match(/^\+\+\+ [ab]\/(.+)$/);
        const hunkMatch = line.match(
          /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/,
        );

        if (fileMatch) {
          filePath = fileMatch[1];
        } else if (fileMatch2) {
          if (!filePath) filePath = fileMatch2[1];
        } else if (hunkMatch) {
          currentHunk = {
            oldStart: Number(hunkMatch[1]),
            oldCount: Number(hunkMatch[2]) || 1,
            newStart: Number(hunkMatch[3]),
            newCount: Number(hunkMatch[4]) || 1,
            lines: [],
          };
          hunks.push(currentHunk);
        } else if (currentHunk && line.startsWith("+")) {
          currentHunk.lines.push({ type: "add", content: line.slice(1) });
        } else if (currentHunk && line.startsWith("-")) {
          currentHunk.lines.push({ type: "remove", content: line.slice(1) });
        } else if (currentHunk && line.startsWith(" ")) {
          currentHunk.lines.push({ type: "context", content: line.slice(1) });
        } else if (currentHunk && line === "") {
          currentHunk.lines.push({ type: "context", content: "" });
        }
      }

      if (filePath) {
        results.push({ filePath, hunks });
      }
    }

    return results;
  }

  applyPatch(content: string, patch: Patch): string {
    const lines = content.split("\n");
    const resultLines = [...lines];
    let offset = 0;

    for (const hunk of patch.hunks) {
      const hunkResult = this.applyHunk(resultLines, hunk, offset);
      resultLines.length = 0;
      resultLines.push(...hunkResult.lines);
      offset = hunkResult.offset;
    }

    return resultLines.join("\n");
  }

  private findHunkPosition(fileLines: string[], hunk: Hunk, offset: number): number {
    const nominalIndex = hunk.oldStart - 1 + offset;
    const contextAndRemove = hunk.lines.filter(
      (l) => l.type === "context" || l.type === "remove",
    );

    if (contextAndRemove.length === 0) return Math.max(0, nominalIndex);

    const firstNonEmpty = contextAndRemove.find((l) => l.content.trim() !== "");
    if (!firstNonEmpty) return Math.max(0, nominalIndex);

    if (nominalIndex >= 0 && nominalIndex < fileLines.length) {
      if (fileLines[nominalIndex].trim() === firstNonEmpty.content.trim()) {
        return nominalIndex;
      }
    }

    const searchRadius = 10;
    const start = Math.max(0, nominalIndex - searchRadius);
    const end = Math.min(fileLines.length, nominalIndex + searchRadius);

    for (let pos = start; pos < end; pos++) {
      if (fileLines[pos].trim() === firstNonEmpty.content.trim()) {
        let match = true;
        for (let ci = 1; ci < contextAndRemove.length; ci++) {
          const target = contextAndRemove[ci];
          if (target.content.trim() !== "" && pos + ci < fileLines.length) {
            if (fileLines[pos + ci].trim() !== target.content.trim()) {
              match = false;
              break;
            }
          }
        }
        if (match) return pos;
      }
    }

    for (let pos = 0; pos < fileLines.length; pos++) {
      if (fileLines[pos].trim() === firstNonEmpty.content.trim()) {
        let match = true;
        for (let ci = 1; ci < contextAndRemove.length; ci++) {
          const target = contextAndRemove[ci];
          if (target.content.trim() !== "" && pos + ci < fileLines.length) {
            if (fileLines[pos + ci].trim() !== target.content.trim()) {
              match = false;
              break;
            }
          }
        }
        if (match) return pos;
      }
    }

    return nominalIndex;
  }

  private applyHunk(
    fileLines: string[],
    hunk: Hunk,
    initialOffset: number,
  ): { lines: string[]; offset: number } {
    const result: string[] = [];
    const hunkIndex = this.findHunkPosition(fileLines, hunk, initialOffset);
    let offset = initialOffset;
    const oldLineCount = hunk.lines.filter(
      (l: HunkLine) => l.type === "context" || l.type === "remove",
    ).length;

    for (let i = 0; i < fileLines.length; i++) {
      if (i === hunkIndex) {
        for (const hLine of hunk.lines) {
          if (hLine.type === "context") {
            result.push(hLine.content);
          } else if (hLine.type === "add") {
            result.push(hLine.content);
          }
        }
        const removeCount = hunk.lines.filter((l: HunkLine) => l.type === "remove").length;
        const addCount = hunk.lines.filter((l: HunkLine) => l.type === "add").length;
        offset += addCount - removeCount;
        i += oldLineCount - 1;
      } else {
        result.push(fileLines[i]);
      }
    }

    return { lines: result, offset };
  }

  detectConflicts(content: string, patch: Patch): string[] {
    const lines = content.split("\n");
    const conflicts: string[] = [];

    for (const hunk of patch.hunks) {
      const contextAndRemove = hunk.lines.filter(
        (l) => l.type === "context" || l.type === "remove",
      );

      if (contextAndRemove.length === 0) continue;

      const firstNonEmpty = contextAndRemove.find((l) => l.content.trim() !== "");
      if (!firstNonEmpty) continue;

      let found = false;
      for (let pos = 0; pos < lines.length; pos++) {
        if (lines[pos].trim() === firstNonEmpty.content.trim()) {
          let match = true;
          for (let ci = 1; ci < contextAndRemove.length; ci++) {
            const target = contextAndRemove[ci];
            if (target.content.trim() !== "" && pos + ci < lines.length) {
              if (lines[pos + ci].trim() !== target.content.trim()) {
                match = false;
                break;
              }
            }
          }
          if (match) {
            found = true;
            break;
          }
        }
      }

      if (!found) {
        conflicts.push(
          `Could not find matching context for hunk at line ${hunk.oldStart} in ${patch.filePath}`,
        );
      }
    }

    return conflicts;
  }

  generateDiff(oldContent: string, newContent: string, filePath: string): string {
    const oldLines = oldContent.split("\n");
    const newLines = newContent.split("\n");
    const diff: string[] = [];

    diff.push(`--- a/${filePath}`);
    diff.push(`+++ b/${filePath}`);

    let i = 0;
    let j = 0;
    const hunkLines: string[] = [];
    let hunkOldStart = 0;
    let hunkNewStart = 0;
    let hunkOldCount = 0;
    let hunkNewCount = 0;
    let inHunk = false;

    while (i < oldLines.length || j < newLines.length) {
      if (i < oldLines.length && j < newLines.length && oldLines[i] === newLines[j]) {
        if (inHunk) {
          hunkLines.push(` ${oldLines[i]}`);
          hunkOldCount++;
          hunkNewCount++;
        }
        i++;
        j++;
      } else if (inHunk || Math.abs(i - j) === 0 || !inHunk) {
        if (!inHunk) {
          hunkOldStart = i + 1;
          hunkNewStart = j + 1;
          hunkOldCount = 0;
          hunkNewCount = 0;
          inHunk = true;
        }
        if (i < oldLines.length && (j >= newLines.length || oldLines[i] !== newLines[j])) {
          hunkLines.push(`-${oldLines[i]}`);
          hunkOldCount++;
          i++;
        }
        if (j < newLines.length && (i >= oldLines.length || oldLines[i] !== newLines[j])) {
          hunkLines.push(`+${newLines[j]}`);
          hunkNewCount++;
          j++;
        }
      } else {
        i++;
        j++;
      }
    }

    if (inHunk) {
      diff.push(
        `@@ -${hunkOldStart},${hunkOldCount} +${hunkNewStart},${hunkNewCount} @@`,
      );
      diff.push(...hunkLines);
    }

    return diff.join("\n");
  }

  createPatch(
    id: string,
    filePath: string,
    hunks: Hunk[],
  ): Patch {
    return {
      id,
      filePath,
      hunks,
      status: "pending",
      createdAt: new Date(),
    };
  }
}
