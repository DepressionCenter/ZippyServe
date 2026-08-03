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
**Prototype.** A small, read-only MCP server is implemented and opt-in via the `-mcp` flag. The core tool set (`get_server_info`, `list_files`, `get_recent_requests`) is read-only server-side introspection. Optional browser-side instrumentation — console/error capture, exposed via `get_console_log` — is available behind the additional `-mcp-browser` flag; see [Browser instrumentation](#browser-instrumentation) below.

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
- **Localhost-only, permanently.** Rides the same `127.0.0.1`-bound listener as file serving (`main.go`'s `http.Server.Addr`) — never exposed on a network interface. This is a closed decision, not subject to a future "should we allow non-localhost" request; see Open questions below for the reasoning.
- **Reserved path, always intercepted.** `/__mcp` (and its `/__mcp/report`, `/__mcp/inject.js` sub-paths — see Browser instrumentation) is checked before any file-serving logic runs, whether or not `-mcp`/`-mcp-browser` is enabled (returns 404 when disabled), so a served file can never collide with or shadow it.
- **Read-only.** No tool mutates the filesystem, the server, or anything else. This avoids needing any additional authorization model. See Excluded by design below for tools that were deliberately not built because they'd break this guarantee.
- **Reuses existing traversal guards.** `list_files`'s `path` argument goes through the same `filepath.Clean`-under-root approach as file serving, so it can't escape the served root.
- **Bounded output.** Request log and file listings are capped (200 and 500 entries respectively) so a tool call can't produce an unbounded response.
- **Browser instrumentation is a separate opt-in.** `-mcp-browser` gates script-injection and report-ingestion independently of `-mcp`, because it modifies bytes served to every HTML page and can silently interact badly with a page's own strict CSP — a bigger, more invasive change than the read-only tools above, so it gets its own flag rather than being folded into `-mcp`. The server refuses to start (`log.Fatalf`) if `-mcp-browser` is passed without `-mcp`.
- **`POST /__mcp/report` validates `Origin`.** `ServeHTTP` sets `Access-Control-Allow-Origin: *` on every response (pre-existing, unrelated to this endpoint) — but that header only controls whether a cross-origin script may *read* a response, not whether its request is *sent and takes effect*. Without an additional check, any origin's JS could POST fabricated events into a log an AI agent later reads and trusts (a confused-deputy / log-poisoning risk). `/__mcp/report` therefore requires `Origin` to equal `http://127.0.0.1:<port>` or `http://localhost:<port>` (both are this server instance — it binds `127.0.0.1` only, but the shipped run scripts open the browser via the `localhost` hostname); missing or mismatched `Origin` is a 403.
- **Bounded ingestion.** `/__mcp/report` request bodies are capped at 64 KB (one event, not a batch), and every field is additionally length-capped server-side before being stored (message 4000 bytes, stack 8000 bytes, source/pageUrl 2000 bytes).
- **Known residual risk: page CSP.** ZippyServe doesn't control or inspect a served page's own Content-Security-Policy (e.g. a `<meta http-equiv="Content-Security-Policy">` tag in the page's own HTML). If that policy's `script-src` doesn't include `'self'`, the browser silently blocks the injected `<script src="/__mcp/inject.js">` tag — the page loads and runs normally, it's just not instrumented. This is undetectable from the server side and is a known, accepted limitation of this opt-in feature, not a bug.

## Prototype tool set
The first three are read-only and require no browser-side changes. The fourth, `get_console_log`, depends on the optional browser instrumentation described below and additionally requires `-mcp-browser`.

| Tool | Purpose |
|---|---|
| `get_server_info` | Port, serving root, index file, version, uptime. |
| `list_files` | Recursive listing (path, size, mtime) of the served root or a subdirectory — lets an agent see what's servable without shelling out. |
| `get_recent_requests` | Tail of an in-memory ring buffer of recent HTTP requests (method, path, status, duration). Lets an agent driving the app in its own browser correlate its actions with what the server actually saw (404s, unexpected methods, etc.) — this is the actual "debugging" value of the prototype. |
| `get_console_log` | Recent browser console output, uncaught errors, and unhandled promise rejections captured via the optional browser instrumentation. Requires `-mcp-browser`; returns an error result otherwise. |

## Browser instrumentation
Implemented, opt-in via `-mcp-browser` (which requires `-mcp` also be set — the server `log.Fatalf`s at startup otherwise, failing closed on misconfiguration since this flag changes every byte of HTML served, a materially bigger behavior change than the read-only tools above).

### Transport: POST-and-buffer, not WebSocket
`get_console_log` is pulled on demand, the same way `get_recent_requests` already is — nothing here needs server-initiated push. A WebSocket is straightforward with stdlib `net/http` hijacking in principle, but hand-rolling framing, ping/pong, and concurrent-writer safety correctly is materially more code and state than this debug tool needs. So, consistent with the rest of this server's hand-rolled JSON-RPC-over-HTTP approach and the zero-dependency constraint, two more reserved sub-paths were added under the existing `/__mcp` prefix:
- `POST /__mcp/report` — the injected script posts one event at a time, fire-and-forget, via `navigator.sendBeacon` (falling back to `fetch(..., {keepalive:true})`).
- `GET /__mcp/inject.js` — serves the instrumentation script as an external file, not inlined into the injected `<script>` tag, so a page's own CSP `script-src` only needs to allow `'self'` — not `'unsafe-inline'` — for the script to load.

Both sub-paths 404 when `-mcp-browser` is off, even if `-mcp` is on — same "reserved path, 404 when disabled" posture as `/__mcp` itself.

### Injection mechanism
`injectBrowserScript` (`src/server/instrument.go`) inserts `<script src="/__mcp/inject.js"></script>` immediately after `<head ...>` (case-insensitive), falling back to immediately before `</body>`, falling back to appending at the end of the document for a bare fragment. Applied in `serveFile` (`main.go`) in two places when `-mcp-browser` is on: (a) a new `.html`/`.htm` branch (previously these fell through untouched to `http.ServeFile`), and (b) the existing Markdown-rendering branch's output. **Trade-off:** the new `.html`/`.htm` branch reads the whole file and rewrites it manually instead of using `http.ServeFile`, which means Range/If-Modified-Since support is lost for `.html`/`.htm` specifically — but only while `-mcp-browser` is enabled.

### Captured signals
Exactly three categories: `window.onerror` (message, source, line, col, `error.stack` when available), `unhandledrejection` (`reason.message`/`.stack` when Error-like, else `String(reason)`), and wrapped `console.log/info/warn/error/debug` (level + stringified args — still calls through to the original method, so the developer's own devtools output is unaffected). **Explicitly out of scope:** wrapping `fetch`/`XMLHttpRequest` to catch client-observed failed requests. Server-observed request outcomes are already covered by `get_recent_requests`; client-side fetch/XHR interception is left as a follow-up (see Open questions).

### Buffering
A single global ring buffer (`consoleLog`, `src/server/instrument.go`), the same pattern as `requestLog`, bounded at 500 entries (chattier than the request log's 200, since a page under active development logs far more than the server sees requests). Each entry carries the reporting page's URL and a server-stamped UTC timestamp, so an agent can tell entries from different pages/reloads apart without full per-session infrastructure — a reasonable default for a local, single-developer tool, not a gap being deferred.

## Excluded by design
These tools from the earlier `docs/MCP-server-specs.md` RFC were deliberately **not** built, as a permanent decision rather than deferred work:
- **`mock_api_route`** (fake response bodies/status codes) and **`throttle_asset`** (injected latency/packet loss) both mutate server behavior, unlike every other tool here. ZippyServe's MCP server stays strictly read-only — no tool changes what gets served or how, even in-memory-only and even reset-on-restart. This is a deliberate, permanent line, not a gap to fill later.
- **`list_web_root`** and **`get_recent_logs`** from that RFC are already satisfied by `list_files` and `get_recent_requests` respectively — no duplicate tools were added.
- **`read_live_browser_errors`** (described there as WebSocket-based real-time streaming) is satisfied by `get_console_log` above — pull-based rather than push, per the Transport rationale.

## Open questions for follow-up
- Should the MCP endpoint's bind extend to non-localhost use (e.g. serving to a container/VM where the agent runs elsewhere)? **Closed: no.** ZippyServe's MCP server will never bind beyond `127.0.0.1`. An agent that needs to reach a containerized ZippyServe instance should run in the same container/network namespace and talk to it via `localhost` — not request network exposure of the port.
- Should `get_recent_requests` support filtering (by status code, path prefix) once real usage shows what agents actually need?
- Should the injected script also wrap `fetch`/`XMLHttpRequest` to capture client-observed failed network requests (timeouts, CORS failures, non-2xx responses the page's own JS sees)? Deliberately out of scope for now — `get_recent_requests` already covers server-observed outcomes, and this would add a second interception surface with its own edge cases (aborted requests, streamed bodies). Revisit if real usage shows a gap.
- Should ZippyServe detect and surface (e.g. at startup, or via `get_server_info`) when a served page's own CSP would silently block the injected script, rather than leaving it as an undetectable failure mode? Would likely require parsing `<meta http-equiv="Content-Security-Policy">` tags in served HTML — not attempted here.
