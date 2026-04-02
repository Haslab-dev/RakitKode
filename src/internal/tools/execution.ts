import type { Capability } from "../capability/types.ts";
import type { ExecutionResult } from "../../types.ts";

export class RunCommandTool implements Capability {
  name = "run_command";
  description = "Execute a shell command and return stdout, stderr, and exit code.";
  parameters = {
    type: "object",
    properties: {
      command: { type: "string", description: "Shell command to execute" },
      timeout: { type: "number", description: "Timeout in milliseconds (default: 120000)" },
      cwd: { type: "string", description: "Working directory" },
    },
    required: ["command"],
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const command = input.command as string;
    const timeout = (input.timeout as number) || 120000;
    const cwd = (input.cwd as string) || process.cwd();

    if (!command) throw new Error("command is required");

    const proc = Bun.spawn(["sh", "-c", command], {
      stdout: "pipe",
      stderr: "pipe",
      cwd,
    });

    const stdout = await new Response(proc.stdout).text();
    const stderr = await new Response(proc.stderr).text();

    const exitCode = await proc.exited;

    return {
      stdout,
      stderr,
      exitCode,
      success: exitCode === 0,
    } satisfies ExecutionResult;
  }
}

export class RunTestsTool implements Capability {
  name = "run_tests";
  description = "Run project tests. Auto-detects test runner (bun, npm, cargo, go, pytest).";
  parameters = {
    type: "object",
    properties: {
      command: { type: "string", description: "Explicit test command to run (auto-detected if not provided)" },
      cwd: { type: "string", description: "Working directory for the test command" },
    },
  };

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const testCommand = (input.command as string) || null;
    const cwd = (input.cwd as string) || ".";

    let command = testCommand;

    if (!command) {
      command = await this.detectTestRunner(cwd);
    }

    if (!command) {
      throw new Error("Could not detect test runner. Provide a command explicitly.");
    }

    const proc = Bun.spawn(["sh", "-c", command], {
      stdout: "pipe",
      stderr: "pipe",
      cwd,
    });

    const stdout = await new Response(proc.stdout).text();
    const stderr = await new Response(proc.stderr).text();
    const exitCode = await proc.exited;

    return {
      stdout,
      stderr,
      exitCode,
      success: exitCode === 0,
    } satisfies ExecutionResult;
  }

  private async detectTestRunner(cwd: string): Promise<string | null> {
    try {
      if (await Bun.file(`${cwd}/bun.lockb`).exists() || await Bun.file(`${cwd}/bun.lock`).exists()) {
        return "bun test";
      }
      if (await Bun.file(`${cwd}/package.json`).exists()) {
        return "npm test";
      }
      if (await Bun.file(`${cwd}/Cargo.toml`).exists()) {
        return "cargo test";
      }
      if (await Bun.file(`${cwd}/go.mod`).exists()) {
        return "go test ./...";
      }
      if (await Bun.file(`${cwd}/pytest.ini`).exists() || await Bun.file(`${cwd}/setup.py`).exists()) {
        return "pytest";
      }
    } catch {
      return "npm test";
    }
    return null;
  }
}

export class StreamCommandTool implements Capability {
  name = "stream_command";
  description = "Execute a shell command with streaming output callbacks";

  private onStdout?: (data: string) => void;
  private onStderr?: (data: string) => void;
  private onExit?: (code: number) => void;

  setCallbacks(callbacks: {
    onStdout?: (data: string) => void;
    onStderr?: (data: string) => void;
    onExit?: (code: number) => void;
  }): void {
    this.onStdout = callbacks.onStdout;
    this.onStderr = callbacks.onStderr;
    this.onExit = callbacks.onExit;
  }

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const command = input.command as string;
    if (!command) throw new Error("command is required");

    const proc = Bun.spawn(["sh", "-c", command], {
      stdout: "pipe",
      stderr: "pipe",
    });

    const stdoutReader = proc.stdout.getReader();
    const stderrReader = proc.stderr.getReader();

    const readStream = async (
      reader: ReadableStreamDefaultReader<Uint8Array>,
      type: "stdout" | "stderr",
    ) => {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        const text = new TextDecoder().decode(value);
        if (type === "stdout" && this.onStdout) this.onStdout(text);
        if (type === "stderr" && this.onStderr) this.onStderr(text);
      }
    };

    await Promise.all([readStream(stdoutReader, "stdout"), readStream(stderrReader, "stderr")]);

    const exitCode = await proc.exited;
    if (this.onExit) this.onExit(exitCode);

    return { exitCode, success: exitCode === 0 };
  }
}
