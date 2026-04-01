import type { Capability } from "../capability/types.ts";
import { PatchEngine } from "../patch/engine.ts";
import { PatchManager } from "../patch/manager.ts";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { randomUUID } from "node:crypto";

export class ApplyPatchTool implements Capability {
  name = "apply_patch";
  description = "Apply a unified diff patch to make small targeted edits to a file. Use write_file instead for full file rewrites or if you don't know the exact current file content.";
  parameters = {
    type: "object",
    properties: {
      diff: { type: "string", description: "Unified diff content to apply" },
      dryRun: { type: "boolean", description: "Preview changes without applying (default: false)" },
    },
    required: ["diff"],
  };

  private patchManager: PatchManager;

  constructor(patchManager: PatchManager) {
    this.patchManager = patchManager;
  }

  private get patchEngine() {
    return this.patchManager.getPatchEngine();
  }

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const diff = input.diff as string;
    const dryRun = !!input.dryRun;

    if (!diff) throw new Error("diff is required");

    try {
      const parsed = this.patchEngine.parseUnifiedDiff(diff);
      if (parsed.length === 0) {
        throw new Error("No valid unified diff found in the input");
      }

      const results: any[] = [];

      for (const { filePath, hunks } of parsed) {
        const absolutePath = resolve(this.patchManager.getMemory().cwd, filePath);
        
        if (!existsSync(absolutePath)) {
          results.push({
            filePath,
            success: false,
            error: `File not found: ${absolutePath}`,
          });
          continue;
        }

        const content = readFileSync(absolutePath, "utf-8");
        const patch = this.patchEngine.createPatch(randomUUID(), filePath, hunks);
        patch.originalContent = content; // Store for revert
        const conflicts = this.patchEngine.detectConflicts(content, patch);

        if (conflicts.length > 0) {
          results.push({
            filePath,
            success: false,
            error: "Conflicts detected",
            conflicts,
          });
          continue;
        }

        // Register with PatchManager for TUI
        const registered = this.patchManager.addPatch(filePath, hunks, "modified", "pending", content);

        if (!dryRun) {
          const newContent = this.patchEngine.applyPatch(content, patch);
          writeFileSync(absolutePath, newContent, "utf-8");
          results.push({ filePath, success: true, applied: true, patchId: registered.id });
        } else {
          results.push({ filePath, success: true, applied: false, patchId: registered.id });
        }
      }

      const finalSuccess = results.every((r) => r.success);
      return {
        success: finalSuccess,
        results,
        summary: `${results.filter((r) => r.success).length}/${results.length} patches applied`,
      };
    } catch (err: any) {
      return { success: false, error: err.message || "Failed to apply patch" };
    }
  }
}
