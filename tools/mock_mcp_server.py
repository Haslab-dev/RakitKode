#!/usr/bin/env python3
"""
Dummy MCP Server implementing Model Context Protocol JSON-RPC 2.0 over stdio.
Provides mock tools (calculator, echo, system_info), resources, and prompts.
"""

import sys
import json
import time
import os

def send_response(response):
    payload = json.dumps(response)
    sys.stdout.write(payload + "\n")
    sys.stdout.flush()

def handle_request(req):
    msg_id = req.get("id")
    method = req.get("method")
    params = req.get("params") or {}

    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "protocolVersion": "2024-11-05",
                "serverInfo": {
                    "name": "mock-mcp-server",
                    "version": "1.0.0"
                },
                "capabilities": {
                    "tools": {},
                    "resources": {},
                    "prompts": {}
                }
            }
        }

    elif method == "tools/list":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "tools": [
                    {
                        "name": "mock_calculator",
                        "description": "Evaluate basic arithmetic expressions safely",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "expression": {
                                    "type": "string",
                                    "description": "Math expression (e.g. '40 + 2')"
                                }
                            },
                            "required": ["expression"]
                        }
                    },
                    {
                        "name": "mock_echo",
                        "description": "Echo input text with timestamp and server metadata",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "message": {
                                    "type": "string",
                                    "description": "Text message to echo"
                                }
                            },
                            "required": ["message"]
                        }
                    },
                    {
                        "name": "mock_system_info",
                        "description": "Return mock host environment metrics and status",
                        "inputSchema": {
                            "type": "object",
                            "properties": {}
                        }
                    }
                ]
            }
        }

    elif method == "tools/call":
        name = params.get("name")
        args = params.get("arguments") or {}

        if name == "mock_calculator":
            expr = args.get("expression", "0")
            try:
                # safe arithmetic eval
                allowed = set("0123456789+-*/(). ")
                if not all(c in allowed for c in expr):
                    raise ValueError("unsafe characters in expression")
                result_val = eval(expr, {"__builtins__": None}, {})
                text_out = f"Result of '{expr}': {result_val}"
            except Exception as e:
                text_out = f"Error evaluating '{expr}': {e}"
            return {
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "content": [{"type": "text", "text": text_out}],
                    "isError": False
                }
            }

        elif name == "mock_echo":
            msg = args.get("message", "")
            text_out = f"[Mock MCP Echo @ {time.strftime('%Y-%m-%d %H:%M:%S')}] {msg}"
            return {
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "content": [{"type": "text", "text": text_out}],
                    "isError": False
                }
            }

        elif name == "mock_system_info":
            info = {
                "server": "mock-mcp-server",
                "status": "healthy",
                "uptime_seconds": 1337,
                "pid": os.getpid(),
                "cwd": os.getcwd()
            }
            return {
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "content": [{"type": "text", "text": json.dumps(info, indent=2)}],
                    "isError": False
                }
            }
        else:
            return {
                "jsonrpc": "2.0",
                "id": msg_id,
                "error": {
                    "code": -32601,
                    "message": f"Method not found: {name}"
                }
            }

    elif method == "resources/list":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "resources": [
                    {
                        "uri": "mock://system/status",
                        "name": "System Status",
                        "description": "Mock system health and diagnostics",
                        "mimeType": "application/json"
                    }
                ]
            }
        }

    elif method == "resources/read":
        uri = params.get("uri")
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "contents": [
                    {
                        "uri": uri,
                        "mimeType": "application/json",
                        "text": json.dumps({"status": "ok", "service": "mock-mcp", "latency_ms": 1.2})
                    }
                ]
            }
        }

    elif method == "prompts/list":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "prompts": [
                    {
                        "name": "mock_code_review",
                        "description": "Generate a code review checklist",
                        "arguments": [
                            {"name": "language", "description": "Programming language", "required": True}
                        ]
                    }
                ]
            }
        }

    elif method == "prompts/get":
        name = params.get("name")
        args = params.get("arguments") or {}
        lang = args.get("language", "general")
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "description": f"Code review template for {lang}",
                "messages": [
                    {
                        "role": "user",
                        "content": {
                            "type": "text",
                            "text": f"Review this {lang} code for correctness, security, and edge cases."
                        }
                    }
                ]
            }
        }

    elif method == "ping":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {}
        }

    # Unknown or unhandled
    return {
        "jsonrpc": "2.0",
        "id": msg_id,
        "error": {
            "code": -32601,
            "message": f"Method '{method}' not handled"
        }
    }

def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception:
            continue

        # If it's a notification (no "id"), don't reply unless method requires it
        if "id" not in req:
            continue

        res = handle_request(req)
        if res:
            send_response(res)

if __name__ == "__main__":
    main()
