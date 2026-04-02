import type { Patch, FileChange, Hunk, PatchStatus } from "../../types.ts";
import type { WorkingMemory, MemoryStore } from "../context/memory.ts";
import { PatchEngine } from "../patch/engine.ts";
import { randomUUID } from "node:crypto";

export class PatchManager {
  private patches: Map<string, Patch> = new Map();
  private fileChanges: Map<string, FileChange> = new Map();
  private patchEngine = new PatchEngine();
  private memoryStore: MemoryStore;

  constructor(memoryStore: MemoryStore) {
    this.memoryStore = memoryStore;
  }

  addPatch(
    filePath: string,
    hunks: Hunk[],
    changeType: FileChange["type"] = "modified",
    status: PatchStatus = "pending",
    originalContent?: string,
    providedId?: string,
  ): Patch {
    const id = providedId || randomUUID();
    const patch: Patch = {
      id,
      filePath,
      hunks,
      status,
      originalContent,
      createdAt: new Date(),
    };
    this.patches.set(id, patch);
    this.fileChanges.set(filePath, { path: filePath, type: changeType });
    return patch;
  }

  addPatchFromDiff(diff: string, status: PatchStatus = "pending", originalContent?: string, providedId?: string): Patch[] {
    const parsed = this.patchEngine.parseUnifiedDiff(diff);
    return parsed.map(({ filePath, hunks }) => this.addPatch(filePath, hunks, "modified", status, originalContent, providedId));
  }

  acceptPatch(id: string): void {
    const patch = this.patches.get(id);
    if (patch) patch.status = "accepted";
  }

  rejectPatch(id: string): void {
    const patch = this.patches.get(id);
    if (!patch) return;
    
    patch.status = "rejected";
    
    // Revert file change
    if (patch.originalContent !== undefined) {
      // Restore existing file
      this.memoryStore.resolveFile(patch.filePath).then((fileEntity) => {
        if (fileEntity.found) {
            Bun.write(fileEntity.path, patch.originalContent!);
        }
      });
    } else {
      // If it was a new file (created), delete it
      this.memoryStore.resolveFile(patch.filePath).then((fileEntity) => {
        if (fileEntity.found) {
           Bun.spawnSync(["rm", "-f", fileEntity.path]);
        }
      });
    }
  }

  acceptAll(): void {
    for (const patch of this.patches.values()) {
      if (patch.status === "pending") patch.status = "accepted";
    }
  }

  getMemory(): WorkingMemory {
    return this.memoryStore.getMemory();
  }

  rejectAll(): void {
    for (const patch of this.patches.values()) {
      if (patch.status === "pending") patch.status = "rejected";
    }
  }

  acceptHunk(patchId: string, hunkIndex: number): void {
    const patch = this.patches.get(patchId);
    if (!patch) return;
    const hunk = patch.hunks[hunkIndex];
    if (hunk) {
      const remainingHunks = patch.hunks.filter((_, i) => i !== hunkIndex);
      if (remainingHunks.length === 0) {
        patch.status = "accepted";
      } else {
        patch.hunks = remainingHunks;
      }
    }
  }

  getPatches(status?: PatchStatus): Patch[] {
    const all = [...this.patches.values()];
    if (status) return all.filter((p) => p.status === status);
    return all;
  }

  getAcceptedPatches(): Patch[] {
    return this.getPatches("accepted");
  }

  getPendingPatches(): Patch[] {
    return this.getPatches("pending");
  }

  getFileChanges(): FileChange[] {
    return [...this.fileChanges.values()];
  }

  getChangesByType(): {
    created: FileChange[];
    modified: FileChange[];
    deleted: FileChange[];
  } {
    const created: FileChange[] = [];
    const modified: FileChange[] = [];
    const deleted: FileChange[] = [];

    for (const change of this.fileChanges.values()) {
      switch (change.type) {
        case "created":
          created.push(change);
          break;
        case "modified":
          modified.push(change);
          break;
        case "deleted":
          deleted.push(change);
          break;
      }
    }

    return { created, modified, deleted };
  }

  clear(): void {
    this.patches.clear();
    this.fileChanges.clear();
  }

  getPatchEngine(): PatchEngine {
    return this.patchEngine;
  }
}
