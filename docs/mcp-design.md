<!--
This file is part of ZippyServe
docs/mcp-design.md
Author(s): Gabriel Mongefranco.
Created: 2026-08-02
Summary: Design notes for the built-in MCP (Model Context Protocol) server —
         motivation, transport choice, security model, and the full tool set
         implemented across src/server/mcp.go, instrument.go, files.go,
         assets.go, simulate.go, and secrets.go.
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
GitHub issue [#1](https://github.com/DepressionCenter/ZippyServe/issues/1) asked whether ZippyServe could expose a built-in [MCP](https://modelcontextprotocol.io/) server so AI coding agents can drive, test, and debug an app served by ZippyServe, without needing a browser plug-in. This document captures the design and status of the implementation, spread across `src/server/mcp.go` (JSON-RPC plumbing and tool registry), `instrument.go` (browser instrumentation), `files.go`, `assets.go`, `simulate.go`, and `secrets.go` (the additional read-only tools).

## Status
**Implemented.** A read-only MCP server is opt-in via the `-mcp` flag, exposing nine server-side introspection/analysis tools plus a tenth (`get_console_log`) that additionally requires `-mcp-browser` for browser-side console/error capture; see [Browser instrumentation](#browser-instrumentation) below. Started as a small prototype (`get_server_info`, `list_files`, `get_recent_requests`); the tool set below reflects the current, complete implementation.

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
- **Bounded output.** Request log and file listings are capped (200 and 500 entries respectively) so a tool call can't produce an unbounded response. Every Stage 2 tool below follows the same posture — see Additional read-only tools.
- **`simulate_request`/`inspect_response_headers` cannot be used for SSRF.** Both run entirely in-process via `httptest.NewRecorder` against this server's own `ServeHTTP` — never a real network dial — so the `path` argument, regardless of content, can never cause an outbound connection. `path` must be relative (leading `/`, no `://`) and cannot target the reserved `/__mcp` prefix; only `GET`/`HEAD` are allowed.
- **Browser instrumentation is a separate opt-in.** `-mcp-browser` gates script-injection and report-ingestion independently of `-mcp`, because it modifies bytes served to every HTML page and can silently interact badly with a page's own strict CSP — a bigger, more invasive change than the read-only tools above, so it gets its own flag rather than being folded into `-mcp`. The server refuses to start (`log.Fatalf`) if `-mcp-browser` is passed without `-mcp`.
- **`POST /__mcp/report` validates `Origin`.** `ServeHTTP` sets `Access-Control-Allow-Origin: *` on every response (pre-existing, unrelated to this endpoint) — but that header only controls whether a cross-origin script may *read* a response, not whether its request is *sent and takes effect*. Without an additional check, any origin's JS could POST fabricated events into a log an AI agent later reads and trusts (a confused-deputy / log-poisoning risk). `/__mcp/report` therefore requires `Origin` to equal `http://127.0.0.1:<port>` or `http://localhost:<port>` (both are this server instance — it binds `127.0.0.1` only, but the shipped run scripts open the browser via the `localhost` hostname); missing or mismatched `Origin` is a 403.
- **Bounded ingestion.** `/__mcp/report` request bodies are capped at 64 KB (one event, not a batch), and every field is additionally length-capped server-side before being stored (message 4000 bytes, stack 8000 bytes, source/pageUrl 2000 bytes).
- **Known residual risk: page CSP.** ZippyServe doesn't control or inspect a served page's own Content-Security-Policy (e.g. a `<meta http-equiv="Content-Security-Policy">` tag in the page's own HTML). If that policy's `script-src` doesn't include `'self'`, the browser silently blocks the injected `<script src="/__mcp/inject.js">` tag — the page loads and runs normally, it's just not instrumented. This is undetectable from the server side and is a known, accepted limitation of this opt-in feature, not a bug.

## Tool set
All ten tools are read-only. The first nine work under `-mcp` alone; `get_console_log` additionally requires `-mcp-browser`.

| Tool | Purpose |
|---|---|
| `get_server_info` | Port, serving root, index file, version, uptime. |
| `list_files` | Recursive listing (path, size, mtime) of the served root or a subdirectory — lets an agent see what's servable without shelling out. |
| `get_recent_requests` | Tail of an in-memory ring buffer of recent HTTP requests (method, path, status, duration). Lets an agent driving the app in its own browser correlate its actions with what the server actually saw (404s, unexpected methods, etc.). |
| `get_console_log` | Recent browser console output, uncaught errors, and unhandled promise rejections captured via the optional browser instrumentation. Requires `-mcp-browser`; returns an error result otherwise. |
| `read_served_file` | Raw text content of a single served file (max 10 MB, UTF-8 only — refuses rather than truncates or garbles). |
| `get_asset_metrics` | Raw size, in-memory gzip size, and compression ratio for a file or directory of served assets. No brotli — see Additional read-only tools. |
| `validate_source_maps` | Structural validity of a `.map` file, or a `.js`/`.css` file's `sourceMappingURL` comment and the map it references. |
| `simulate_request` | Status, headers, and a body preview for a path, evaluated in-process (no real network call). |
| `inspect_response_headers` | Response headers for a path, optionally filtered to one category (cache/cors/security/cookies/encoding). |
| `scan_for_secrets` | Heuristic scan of served text files for credential-shaped patterns. Output is fully redacted — file/line/rule only. |

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

## Additional read-only tools
Six more tools beyond the original prototype three, all in `src/server/`, all under `-mcp` alone (no new flag — none mutate the filesystem, server state, or make outbound network calls):

- **`read_served_file`** (`files.go`) and **`list_files`** (`mcp.go`) share one traversal-guard implementation, `resolveServedPath` — extracted from `list_files`'s original inline check so there is exactly one vetted implementation instead of duplicated `filepath.Clean`/`..`/NUL checks.
- **`get_asset_metrics`** (`assets.go`) reports raw size and gzip size (`compress/gzip`, stdlib) with a compression ratio, plus a compute-time figure documented explicitly as a proxy for serving cost, not a network transfer-time measurement (localhost transfer time isn't meaningful to measure). **No brotli**: Go's standard library has no brotli encoder, and vendoring one would violate the zero-dependency constraint — this is a permanent scope limit.
- **`validate_source_maps`** (`assets.go`) parses `.map` JSON for required fields (`version`, `sources`, `mappings`) and, for `.js`/`.css` input, locates the `sourceMappingURL` comment and validates the map it points to (resolved through `resolveServedPath`, so it can't escape the served root either). Reports plain-English issues, never raw file contents beyond what's needed to name the problem.
- **`simulate_request`** and **`inspect_response_headers`** (`simulate.go`) share `runInternalRequest`, which calls this server's own `ServeHTTP` via `httptest.NewRecorder` — see the Security model's SSRF bullet above for why this can never make a real network call regardless of input. `simulate_request`'s response body is capped at 64 KB.
- **`scan_for_secrets`** (`secrets.go`) is a small, hand-picked set of high-confidence regex rules (AWS access key IDs, PEM private key headers, GitHub/Slack tokens, generic `api_key`/`secret`/`token` assignments) — not an exhaustive secret-scanning product, and a clean result is not proof of absence of secrets. **Output is fully redacted**: results contain only `file`, `line`, and `rule` — the matched text itself is never included in the response, logged, or otherwise surfaced. This was a deliberate design decision: a tool whose purpose is finding secrets must not itself become a channel that pipes live credentials into an agent's context. Files over 2 MB and known binary/media extensions are skipped (wasted work, not a security gap — hand-typed secrets don't end up in compiled binaries). Results capped at 200 matches.

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
