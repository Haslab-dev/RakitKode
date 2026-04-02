import type { Capability } from "../capability/types.ts";

export interface MCPServerConfig {
  type?: "stdio" | "remote";
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
}

export interface MCPTool {
  name: string;
  description: string;
  inputSchema: any;
}

export class MCPClient {
  public serverName: string;
  private config: MCPServerConfig;
  private process: any;
  private requestId = 0;
  private pendingRequests = new Map<number | string, { resolve: (val: any) => void; reject: (err: any) => void }>();
  private remotePostUrl?: string;

  constructor(serverName: string, config: MCPServerConfig) {
    this.serverName = serverName;
    this.config = config;
  }

  async start(): Promise<void> {
    const type = this.config.type || "stdio";
    if (type === "stdio") {
      if (!this.config.command) throw new Error(`MCP Server ${this.serverName} missing command`);
      this.process = Bun.spawn([this.config.command, ...(this.config.args || [])], {
        env: { ...process.env, ...(this.config.env || {}) },
        stdio: ["pipe", "pipe", "inherit"],
      });
      this.listenStdio();
    } else {
      if (!this.config.url) throw new Error(`MCP Server ${this.serverName} missing URL`);
      try {
        await this.connectRemote();
      } catch (err) {
        // Fallback for Pure HTTP-RPC (no SSE)
        console.warn(`MCP ${this.serverName} does not support SSE, attempting pure HTTP-RPC fallback...`);
      }
    }
    
    // Handshake
    try {
      await this.call("initialize", {
        protocolVersion: "2024-11-05",
        capabilities: {},
        clientInfo: { name: "rakitkode", version: "0.2.0" },
      });
      await this.notify("notifications/initialized", {});
    } catch (err: any) {
      console.error(`MCP Initialization failed for ${this.serverName}:`, err.message);
      throw err; // Crucial for connection state
    }
  }

  private async listenStdio() {
    const reader = this.process.stdout.getReader();
    let buffer = "";
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += new TextDecoder().decode(value);
        let nl;
        while ((nl = buffer.indexOf("\n")) !== -1) {
          const line = buffer.slice(0, nl);
          buffer = buffer.slice(nl + 1);
          if (line.trim()) this.handleMessage(JSON.parse(line));
        }
      }
    } catch (err) {}
  }

  private async connectRemote() {
    const response = await fetch(this.config.url!, {
        method: "GET",
        headers: { "Accept": "text/event-stream" }
    });

    if (!response.ok) {
        // 405 or other likely means it's not SSE
        throw new Error(`SSE not supported: ${response.status}`);
    }
    
    const reader = response.body?.getReader();
    if (!reader) throw new Error("No response body from remote MCP");

    (async () => {
      let buffer = "";
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += new TextDecoder().decode(value);
          let eventEnd;
          while ((eventEnd = buffer.indexOf("\n\n")) !== -1) {
            const eventPart = buffer.slice(0, eventEnd);
            buffer = buffer.slice(eventEnd + 2);
            this.handleSSEEvent(eventPart);
          }
        }
      } catch (err) {
        console.error(`Remote MCP SSE error:`, err);
      }
    })();
  }

  private handleSSEEvent(eventPart: string) {
    const lines = eventPart.split("\n");
    let eventName = "message";
    let data = "";
    for (const line of lines) {
      if (line.startsWith("event: ")) eventName = line.slice(7).trim();
      else if (line.startsWith("data: ")) data += line.slice(6);
    }
    
    if (eventName === "endpoint") {
        const url = new URL(this.config.url!);
        const newUrl = new URL(data.trim(), url.origin);
        newUrl.search = url.search;
        this.remotePostUrl = newUrl.toString();
    } else if (data) {
        try { this.handleMessage(JSON.parse(data)); } catch {}
    }
  }

  private extractSSEData(rawBody: string): string | null {
    for (const line of rawBody.split("\n")) {
      if (line.startsWith("data: ")) return line.slice(6).trim();
    }
    return null;
  }

  private handleMessage(msg: any) {
    const id = msg.id;
    if (id !== undefined) {
      const pending = this.pendingRequests.get(id);
      if (pending) {
        this.pendingRequests.delete(id);
        if (msg.error) pending.reject(new Error(msg.error.message || "MCP Unknown Error"));
        else pending.resolve(msg.result);
      }
    }
  }

  async call(method: string, params: any): Promise<any> {
    const id = `req-${this.requestId++}`;
    const msgObj = { jsonrpc: "2.0", id, method, params };

    if (this.config.type === "remote") {
        const postUrl = this.remotePostUrl || this.config.url!;
        const response = await fetch(postUrl, {
            method: "POST",
            body: JSON.stringify(msgObj),
            headers: { 
                "Content-Type": "application/json",
                "Accept": "application/json, text/event-stream",
                "User-Agent": "RakitKode/0.2.0"
            }
        });

        if (response.status === 406) {
            // Some servers might be very picky, try without Accept if it fails
             const retryResponse = await fetch(postUrl, {
                method: "POST",
                body: JSON.stringify(msgObj),
                headers: { "Content-Type": "application/json", "User-Agent": "RakitKode/0.2.0" }
            });
            if (!retryResponse.ok) throw new Error(`HTTP ${retryResponse.status}: ${retryResponse.statusText}`);
            
            if (!this.remotePostUrl) {
                const rawBody = await retryResponse.text();
                try {
                  const result = JSON.parse(rawBody);
                  if (result.error) throw new Error(result.error.message);
                  return result.result;
                } catch (err) {
                  console.error(`MCP ${this.serverName}: Failed to parse JSON response on retry. Body: ${rawBody.slice(0, 500)}`);
                  throw new Error("Failed to parse JSON response from MCP server");
                }
            }
        } else if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }

        if (!this.remotePostUrl && response.status !== 406) {
            const rawBody = await response.text();
            try {
                const result = JSON.parse(rawBody);
                if (result.error) throw new Error(result.error.message);
                return result.result;
            } catch {
                const data = this.extractSSEData(rawBody);
                if (data) {
                    const result = JSON.parse(data);
                    if (result.error) throw new Error(result.error.message);
                    return result.result;
                }
                console.error(`MCP ${this.serverName}: Failed to parse JSON response. Body was: ${rawBody.slice(0, 500)}`);
                throw new Error("Failed to parse JSON response from MCP server");
            }
        }

        // In SSE (Pattern B), we wait for the message on the stream
        return new Promise((resolve, reject) => {
            this.pendingRequests.set(id, { resolve, reject });
            setTimeout(() => {
                if (this.pendingRequests.get(id)) {
                    this.pendingRequests.delete(id);
                    reject(new Error("Timeout waiting for SSE response"));
                }
            }, 30000);
        });
    } else {
        const promise = new Promise((resolve, reject) => {
          this.pendingRequests.set(id, { resolve, reject });
        });
        const msg = JSON.stringify(msgObj) + "\n";
        this.process.stdin.write(new TextEncoder().encode(msg));
        this.process.stdin.flush();
        return promise;
    }
  }

  async notify(method: string, params: any): Promise<void> {
    const msgObj = { jsonrpc: "2.0", method, params };
    if (this.config.type === "remote" && this.remotePostUrl) {
        await fetch(this.remotePostUrl, {
            method: "POST",
            body: JSON.stringify(msgObj),
            headers: { "Content-Type": "application/json" }
        });
    } else if (this.process) {
        const msg = JSON.stringify(msgObj) + "\n";
        this.process.stdin.write(new TextEncoder().encode(msg));
        this.process.stdin.flush();
    }
  }

  async listTools(): Promise<MCPTool[]> {
    const result = await this.call("tools/list", {});
    return result.tools || [];
  }

  async callTool(name: string, args: Record<string, any>): Promise<any> {
    return this.call("tools/call", { name, arguments: args });
  }

  stop() {
    if (this.process) this.process.kill();
  }
}

export class MCPCapability implements Capability {
  constructor(
    private client: MCPClient,
    public name: string,
    public description: string,
    public parameters: any
  ) {}

  async execute(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    try {
        const result = await this.client.callTool(this.name, input);
        if (result.isError) throw new Error(result.content?.[0]?.text || "MCP Tool Error");
        const text = result.content?.map((c: any) => c.text).join("\n") || "";
        return { output: text };
    } catch (err: any) {
        return { error: err.message, output: "" };
    }
  }
}
