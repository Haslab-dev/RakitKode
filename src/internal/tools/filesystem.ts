import type { Capability } from "../capability/types.ts";
import { existsSync } from "node:fs";
import { readFile as fsReadFile, writeFile as fsWriteFile, mkdir, rm, readdir, stat } from "node:fs/promises";
import { resolve, dirname } from "node:path";

export class FileSystemTool implements Capability {
  name = "read_file";
  description = "Read file contents from the filesystem. Returns the file content as a string.";
  parameters = {
    type: "object",
    properties: {
      path: { type: "string", description: "Absolute or relative file path to read" },
    },
    required: ["path"],
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const filePath = input.path as string;
    if (!filePath) throw new Error("path is required");

    const absolutePath = resolve(filePath);
    if (!existsSync(absolutePath)) {
      throw new Error(`File not found: ${absolutePath}`);
    }

    const content = await fsReadFile(absolutePath, "utf-8");
    return { content, path: absolutePath };
  }
}

export class WriteFileTool implements Capability {
  name = "write_file";
  description = "Write content to a file. Use this ONLY for new files. For existing files, use apply_patch instead.";
  parameters = {
    type: "object",
    properties: {
      path: { type: "string", description: "Absolute or relative file path to write" },
      content: { type: "string", description: "The content to write to the file" },
    },
    required: ["path", "content"],
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const filePath = input.path as string;
    const content = input.content as string;
    if (!filePath || content === undefined) throw new Error("path and content are required");

    const absolutePath = resolve(process.cwd(), filePath);
    try {
      const dir = dirname(absolutePath);
      if (!existsSync(dir)) {
        await mkdir(dir, { recursive: true });
      }

      await fsWriteFile(absolutePath, content, "utf-8");
      return { success: true, path: absolutePath };
    } catch (err: any) {
      return { success: false, path: absolutePath, error: err.message };
    }
  }
}

export class ListFilesTool implements Capability {
  name = "list_files";
  description = "List files and directories at a given path. Returns an array of objects with path, name, and isDir fields.";
  parameters = {
    type: "object",
    properties: {
      path: { type: "string", description: "Directory path to list (default: current directory)" },
      recursive: { type: "boolean", description: "Whether to list recursively (default: false)" },
      pattern: { type: "string", description: "Optional glob pattern to filter results" },
    },
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const dirPath = (input.path as string) || ".";
    const recursive = (input.recursive as boolean) || false;
    const pattern = (input.pattern as string) || null;

    const absolutePath = resolve(dirPath);
    if (!existsSync(absolutePath)) {
      throw new Error(`Directory not found: ${absolutePath}`);
    }

    const entries = await this.listFilesRecursive(absolutePath, recursive, pattern);
    return { files: entries };
  }

  private async listFilesRecursive(
    dirPath: string,
    recursive: boolean,
    pattern: string | null,
  ): Promise<Array<{ path: string; name: string; isDir: boolean }>> {
    const entries: Array<{ path: string; name: string; isDir: boolean }> = [];
    const items = await readdir(dirPath);

    for (const item of items) {
      if (item.startsWith(".") && item !== ".env") continue;

      const fullPath = [dirPath, item].join("/");
      const info = await stat(fullPath);

      if (pattern) {
        const regex = new RegExp(pattern);
        if (!regex.test(item)) continue;
      }

      entries.push({
        path: fullPath,
        name: item,
        isDir: info.isDirectory(),
      });

      if (recursive && info.isDirectory()) {
        const subEntries = await this.listFilesRecursive(fullPath, recursive, pattern);
        entries.push(...subEntries);
      }
    }

    return entries;
  }
}

export class DeleteFileTool implements Capability {
  name = "delete_file";
  description = "Delete a file from the filesystem.";
  parameters = {
    type: "object",
    properties: {
      path: { type: "string", description: "File path to delete" },
    },
    required: ["path"],
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const filePath = input.path as string;
    if (!filePath) throw new Error("path is required");

    const absolutePath = resolve(filePath);
    if (!existsSync(absolutePath)) {
      throw new Error(`File not found: ${absolutePath}`);
    }

    await rm(absolutePath);
    return { success: true, path: absolutePath };
  }
}
