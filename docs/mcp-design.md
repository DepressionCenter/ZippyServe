<!--
This file is part of ZippyServe
docs/mcp-design.md
Author(s): Gabriel Mongefranco.
Created: 2026-08-02
Summary: Design notes for the built-in MCP (Model Context Protocol) server —
         motivation, transport choice, security model, and the prototype tool
         set implemented in src/server/mcp.go.
Notes: See README file for documentation and full license information.

Copyright © 2026 The Regents of the University of Michigan

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or (at your option) any later version.
This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU General Public License for more details.
You should have received a copy of the GNU General Public License along
with this program. If not, see <https://www.gnu.org/licenses/>.
-->

# Built-in MCP server: design notes

## Motivation
GitHub issue [#1](https://github.com/DepressionCenter/ZippyServe/issues/1) asked whether ZippyServe could expose a built-in [MCP](https://modelcontextprotocol.io/) server so AI coding agents can drive, test, and debug an app served by ZippyServe, without needing a browser plug-in. This document captures the design and the status of the prototype currently in `src/server/mcp.go`.

## Status
**Prototype.** A small, read-only MCP server is implemented and opt-in via the `-mcp` flag. It proves the concept (transport, tool plumbing, security posture) but does not yet include the browser-side instrumentation that would make it useful for JS-level debugging — see [Deferred work](#deferred-work-browser-instrumentation) below.

## Transport: Streamable HTTP, not stdio
Most MCP servers use the **stdio** transport: the client (the agent's host process) spawns the server as a child process and talks to it over stdin/stdout. That doesn't fit ZippyServe's usage pattern — a developer starts ZippyServe once (via `run-windows.ps1` etc.) and it keeps running as they iterate, independent of whatever agent session they open later. The agent doesn't spawn ZippyServe and can't own its stdio.

Instead, the MCP server is mounted on ZippyServe's **existing HTTP listener**, at the reserved path `/__mcp`, using plain JSON-RPC 2.0 POST requests (a minimal implementation of MCP's [Streamable HTTP transport](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#streamable-http)). This means:
- No second port to manage or firewall.
- An agent already running in another terminal/session can attach to an already-running ZippyServe instance by URL — e.g. `claude mcp add --transport http zippyserve http://127.0.0.1:8010/__mcp`.
- Server-to-client streaming (the SSE half of the spec) isn't implemented, since none of the current tools are long-running or need server-initiated push. If a future tool needs it (e.g. live-tailing the request log), this would need revisiting.

## Zero-dependency constraint
`go.mod` has no external dependencies, and the README markets this explicitly. MCP is JSON-RPC 2.0 over a transport — implementing the small subset needed here (`initialize`, `notifications/initialized`, `tools/list`, `tools/call`) is straightforward with stdlib `encoding/json` and `net/http`, so no MCP SDK was added. This keeps the build, licensing, and supply-chain surface exactly as it is today.

## Security model
- **Off by default.** The endpoint only exists when `-mcp` is passed.
- **Localhost-only.** Rides the same `127.0.0.1`-bound listener as file serving (`main.go`'s `http.Server.Addr`) — never exposed on a network interface.
- **Reserved path, always intercepted.** `/__mcp` is checked before any file-serving logic runs, whether or not `-mcp` is enabled (returns 404 when disabled), so a served file can never collide with or shadow it.
- **Read-only.** No tool mutates the filesystem, the server, or anything else. This avoids needing any additional authorization model for a first cut.
- **Reuses existing traversal guards.** `list_files`'s `path` argument goes through the same `filepath.Clean`-under-root approach as file serving, so it can't escape the served root.
- **Bounded output.** Request log and file listings are capped (200 and 500 entries respectively) so a tool call can't produce an unbounded response.

## Prototype tool set
All three are read-only and require no browser-side changes:

| Tool | Purpose |
|---|---|
| `get_server_info` | Port, serving root, index file, version, uptime. |
| `list_files` | Recursive listing (path, size, mtime) of the served root or a subdirectory — lets an agent see what's servable without shelling out. |
| `get_recent_requests` | Tail of an in-memory ring buffer of recent HTTP requests (method, path, status, duration). Lets an agent driving the app in its own browser correlate its actions with what the server actually saw (404s, unexpected methods, etc.) — this is the actual "debugging" value of the prototype. |

## Deferred work: browser instrumentation
The part of issue #1 this prototype does **not** address is JS-level debugging — seeing `console.*` output, uncaught exceptions, and failed network requests *as the browser sees them*, without a browser extension. Because ZippyServe already generates/serves HTML, the natural mechanism is to inject a small first-party script into HTML responses that:
- Captures `window.onerror`, unhandled promise rejections, and `console.*` calls.
- Reports them back to ZippyServe (e.g. `POST /__zippyserve/report` or a WebSocket).
- Feeds a new `get_console_log` MCP tool.

This is deliberately out of scope for the prototype: it modifies served HTML (a bigger, more invasive change than anything above) and deserves its own design review and a dedicated follow-up issue rather than being bundled in here.

## Open questions for follow-up
- Should the MCP endpoint's bound extend to non-localhost use (e.g. serving to a container/VM where the agent runs elsewhere)? Not needed today; would change the security model above if ever requested.
- Should `get_recent_requests` support filtering (by status code, path prefix) once real usage shows what agents actually need?
