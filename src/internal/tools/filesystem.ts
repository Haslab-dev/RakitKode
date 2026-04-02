import type { Capability } from "../capability/types.ts";
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
    const file = Bun.file(absolutePath);
    
    if (!(await file.exists())) {
      throw new Error(`File not found: ${absolutePath}`);
    }

    const content = await file.text();
    return { content, path: absolutePath };
  }
}

export class WriteFileTool implements Capability {
  name = "write_file";
  description = "Write content to a file. Use for creating new files or rewriting existing files. Always provide the full file content.";
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

    const absolutePath = resolve(filePath);
    try {
      await Bun.write(absolutePath, content);
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
    const pattern = (input.pattern as string) || "*";

    const absolutePath = resolve(dirPath);
    
    const globPattern = recursive ? `**/${pattern}` : pattern;
    const glob = new Bun.Glob(globPattern);
    
    const files: Array<{ path: string; name: string; isDir: boolean }> = [];
    
    for await (const file of glob.scan({ cwd: absolutePath, onlyFiles: false })) {
      if (file.startsWith(".") && !file.includes(".env")) continue;
      if (file.includes("node_modules")) continue;

      const fullPath = resolve(absolutePath, file);
      files.push({
        path: fullPath,
        name: file,
        isDir: false, // Bun.Glob scan doesn't easily distinguish isDir without extra stat
      });
    }

    return { files: files.slice(0, 500) }; // Limit early for LLM safety
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
    Bun.spawnSync(["rm", "-rf", absolutePath]);
    return { success: true, path: absolutePath };
  }
}
