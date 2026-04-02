import type { Capability } from "../capability/types.ts";
import type { PatchEngine } from "../patch/engine.ts";
import type { PatchManager } from "../patch/manager.ts";
import { resolve } from "node:path";

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

      const results: Record<string, unknown>[] = [];

      for (const { filePath, hunks } of parsed) {
        const absolutePath = resolve(this.patchManager.getMemory().cwd, filePath);
        
        const file = Bun.file(absolutePath);
        if (!(await file.exists())) {
          results.push({
            filePath,
            success: false,
            error: `File not found: ${absolutePath}`,
          });
          continue;
        }

        const content = await file.text();
        const conflicts = this.patchEngine.detectConflicts(content, {
          id: "",
          filePath,
          hunks,
          status: "pending",
          createdAt: new Date(),
        });

        if (conflicts.length > 0) {
          results.push({
            filePath,
            success: false,
            error: "Conflicts detected",
            conflicts,
          });
          continue;
        }

        if (!dryRun) {
          const patchId = crypto.randomUUID();
          const patch = this.patchEngine.createPatch(patchId, filePath, hunks);
          patch.originalContent = content;
          const newContent = this.patchEngine.applyPatch(content, patch);
          await Bun.write(absolutePath, newContent);
          results.push({ filePath, success: true, applied: true, patchId });
        } else {
          results.push({ filePath, success: true, applied: false });
        }
      }

      const finalSuccess = results.every((r) => r.success);
      return {
        success: finalSuccess,
        results,
        summary: `${results.filter((r) => r.success).length}/${results.length} patches applied`,
      };
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : "Failed to apply patch";
      return { success: false, error: message };
    }
  }
}
