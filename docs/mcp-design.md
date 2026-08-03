<!--
This file is part of ZippyServe
docs/mcp-design.md
Author(s): Gabriel Mongefranco.
Created: 2026-08-02
Summary: Reference and design notes for the built-in MCP (Model Context
         Protocol) server, for both developers running ZippyServe and AI
         coding agents connecting to it — motivation, usage, transport
         choice, security model, and the complete implemented tool set
         (spread across src/server/mcp.go, instrument.go, files.go,
         assets.go, simulate.go, and secrets.go). This document merges and
         supersedes the earlier docs/MCP-server-specs.md proposal; the
         Excluded by design and per-tool notes below record where the
         actual implementation diverges from that original proposal.
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

Documentation license:
Licensed under GNU Free Documentation License v1.3 or later.
-->

# Built-in MCP server: reference & design notes

## Motivation
GitHub issue [#1](https://github.com/DepressionCenter/ZippyServe/issues/1) asked whether ZippyServe could expose a built-in [MCP](https://modelcontextprotocol.io/) server so AI coding agents can drive, test, and debug an app served by ZippyServe, without needing a browser plug-in. An initial prototype shipped three read-only tools; a follow-up RFC (`docs/MCP-server-specs.md`) proposed a larger surface — "live runtime visibility, asset validation, and client-side error streaming." That RFC's content has been folded into this document, which is now the single reference for the built-in MCP server: what it does, how to use it, and — since the implementation didn't follow the RFC 100% — exactly where and why it diverges.

## Status
**Implemented.** Ten read-only MCP tools, opt-in via the `-mcp` flag; a tenth (`get_console_log`) additionally requires `-mcp-browser`. See [Tool set](#tool-set) below.

## Quick start
- **Enable it.** Pass `-mcp` (and optionally `-mcp-browser`) to the ZippyServe binary directly, or to any of the launch scripts: `.\run-windows.ps1 -Mcp -McpBrowser`, `./run-linux.sh --mcp --mcp-browser`, `./run-mac.command --mcp --mcp-browser`.
- **Attach an MCP client.** ZippyServe doesn't spawn or get spawned by the agent — it's a long-running local HTTP server the agent's MCP client attaches to by URL, e.g.:
  ```
  claude mcp add --transport http zippyserve http://127.0.0.1:8010/__mcp
  ```
- **Or call it directly** with any HTTP client (useful for debugging the MCP server itself, or for a client without built-in MCP support):
  ```
  curl -s -X POST http://127.0.0.1:8010/__mcp -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_server_info","arguments":{}}}'
  ```
  A session typically starts with `initialize`, then `tools/list` to discover the schema, then repeated `tools/call`.

## Transport: Streamable HTTP, not stdio
Most MCP servers use the **stdio** transport: the client (the agent's host process) spawns the server as a child process and talks to it over stdin/stdout. The original RFC's security section preferred exactly this ("eliminates network access entirely") and treated HTTP as a fallback. Stdio doesn't fit ZippyServe's actual usage pattern, though: a developer starts ZippyServe once (via `run-windows.ps1` etc.) and it keeps running as they iterate, independent of whatever agent session they open later. The agent doesn't spawn ZippyServe and can't own its stdio — so the fallback is what got built, with the residual risk that entails (see Security model).

Instead, the MCP server is mounted on ZippyServe's **existing HTTP listener**, at the reserved path `/__mcp`, using plain JSON-RPC 2.0 POST requests (a minimal implementation of MCP's [Streamable HTTP transport](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#streamable-http)). This means:
- No second port to manage or firewall.
- An agent already running in another terminal/session can attach to an already-running ZippyServe instance by URL.
- Server-to-client streaming (the SSE half of the spec) isn't implemented, since none of the current tools are long-running or need server-initiated push. `get_console_log` (see Browser instrumentation) is pull-based for the same reason, rather than the WebSocket the RFC proposed for it.

## Zero-dependency constraint
`go.mod` has no external dependencies, and the README markets this explicitly. MCP is JSON-RPC 2.0 over a transport — implementing the small subset needed here (`initialize`, `notifications/initialized`, `tools/list`, `tools/call`) is straightforward with stdlib `encoding/json` and `net/http`, so no MCP SDK was added. This keeps the build, licensing, and supply-chain surface exactly as it is today, and it's also why several RFC proposals were scaled back or dropped — e.g. no brotli support (`get_asset_metrics`) and no hand-rolled WebSocket (`get_console_log`); see the per-tool notes below.

## Security model
- **Off by default.** The endpoint only exists when `-mcp` is passed.
- **Localhost-only, permanently.** Rides the same `127.0.0.1`-bound listener as file serving (`main.go`'s `http.Server.Addr`) — never exposed on a network interface. This is a closed decision, not subject to a future "should we allow non-localhost" request; see Open questions below for the reasoning.
- **Reserved path, always intercepted.** `/__mcp` (and its `/__mcp/report`, `/__mcp/inject.js` sub-paths — see Browser instrumentation) is checked before any file-serving logic runs, whether or not `-mcp`/`-mcp-browser` is enabled (returns 404 when disabled), so a served file can never collide with or shadow it.
- **Read-only.** No tool mutates the filesystem, the server, or anything else. This avoids needing any additional authorization model. See Excluded by design below for RFC-proposed tools that were deliberately not built because they'd break this guarantee.
- **Traversal guard: `filepath.Clean`-under-root, not `os.DirFS`.** The RFC recommended `os.DirFS(root)` for path validation. The actual implementation instead reuses the same `filepath.Clean`-plus-prefix-check approach main.go's file serving already used before any MCP work (`resolveServedPath` in `mcp.go`, shared by every tool that takes a `path` argument). This blocks classic `..`-based traversal — a request like `../../../etc/passwd` collapses and re-roots under the served directory rather than escaping it (verified). **It does not resolve symlinks.** Verified in this session: a directory junction placed inside the served root, pointing outside it, is followed by both plain HTTP file serving and the MCP tools (confirmed with `read_served_file` and `list_files`), exposing content outside the served root. This is a pre-existing characteristic of ZippyServe's file-serving approach, not something newly introduced by the MCP tools — they just inherited the same guard. It only matters if something (a build tool, a misconfigured `-dir`, deliberate tampering) has already placed a symlink/junction inside the tree being served; `os.DirFS` would not have prevented this either without an explicit symlink-following opt-out, so this wasn't simply "the RFC's recommendation was ignored" — it's an open gap either way. See Open questions.
- **Bounded output, not rate-limited.** Every tool caps how much it returns in one call (see the Tool set and Additional read-only tools notes below for exact numbers). This is a size cap, not a request-rate limit — the RFC's own wording ("Rate Limiting: Cap or rate-limit `get_recent_logs`... max 50 entries") conflated the two; ZippyServe does the former, not the latter. There is no limit on how many times an MCP client can call a tool per second.
- **`simulate_request`/`inspect_response_headers` cannot be used for SSRF.** Both run entirely in-process via `httptest.NewRecorder` against this server's own `ServeHTTP` — never a real network dial — so the `path` argument, regardless of content, can never cause an outbound connection. `path` must be relative (leading `/`, no `://`) and cannot target the reserved `/__mcp` prefix; only `GET`/`HEAD` are allowed. (The RFC additionally proposed configurable request headers, cookies, and a `User-Agent` override for this tool; the implementation doesn't support any of those — only `path` and `method`.)
- **`POST /__mcp` itself has no `Origin` check.** Unlike `/__mcp/report` (see the next bullet), the primary JSON-RPC endpoint does not validate `Origin`. Combined with `Access-Control-Allow-Origin: *` on every response (set unconditionally in `ServeHTTP`, `main.go`, predating the MCP work), a malicious webpage open in the same browser as a developer's running ZippyServe instance could — if it knows the local port — send a same-origin-looking POST (e.g. with `Content-Type: text/plain`, which this server parses as JSON regardless of the declared content type) and read back the result of any read-only tool, including `list_files`/`read_served_file` over the whole served root and `get_server_info`'s absolute filesystem path. All ten tools are read-only, so this is an information-disclosure risk, not a mutation risk — but it's real and currently unmitigated. Documented here rather than silently left out; see Open questions for the fix under consideration (mirroring `/__mcp/report`'s check).
- **`POST /__mcp/report` does validate `Origin`.** Requires `Origin` to equal `http://127.0.0.1:<port>` or `http://localhost:<port>` (both are this server instance — it binds `127.0.0.1` only, but the shipped run scripts open the browser via the `localhost` hostname); missing or mismatched `Origin` is a 403. This exists specifically because report contents are later read by an AI agent via `get_console_log` — an unchecked endpoint would let any origin's JS poison that log with fabricated events (a confused-deputy / log-poisoning risk). Request bodies are capped at 64 KB, and every field is additionally length-capped server-side before being stored (message 4000 bytes, stack 8000 bytes, source/pageUrl 2000 bytes).
- **Browser instrumentation is a separate opt-in.** `-mcp-browser` gates script-injection and report-ingestion independently of `-mcp`, because it modifies bytes served to every HTML page and can silently interact badly with a page's own strict CSP — a bigger, more invasive change than the read-only tools above, so it gets its own flag rather than being folded into `-mcp`. The server refuses to start (`log.Fatalf`) if `-mcp-browser` is passed without `-mcp`.
- **Known residual risk: page CSP.** ZippyServe doesn't control or inspect a served page's own Content-Security-Policy (e.g. a `<meta http-equiv="Content-Security-Policy">` tag in the page's own HTML). If that policy's `script-src` doesn't include `'self'`, the browser silently blocks the injected `<script src="/__mcp/inject.js">` tag — the page loads and runs normally, it's just not instrumented. This is undetectable from the server side and is a known, accepted limitation of this opt-in feature, not a bug.
- **Secret-scanning output is fully redacted.** `scan_for_secrets` returns only `file`/`line`/`rule` for a match, never the matched text — see Additional read-only tools.

## Tool set
All ten tools are read-only. The first nine work under `-mcp` alone; `get_console_log` additionally requires `-mcp-browser`.

| Tool | Purpose |
|---|---|
| `get_server_info` | Port, serving root, index file, version, uptime. |
| `list_files` | Flat, alphabetically sorted listing (path, size, mtime) of the served root or a subdirectory — lets an agent see what's servable without shelling out. (The RFC described this as a "hierarchical tree view"; the actual output is a flat list.) |
| `get_recent_requests` | Tail of an in-memory ring buffer of recent HTTP requests (method, path, status, duration), newest first. Lets an agent driving the app in its own browser correlate its actions with what the server actually saw (404s, unexpected methods, etc.). |
| `get_console_log` | Recent browser console output, uncaught errors, and unhandled promise rejections captured via the optional browser instrumentation. Requires `-mcp-browser`; returns an error result otherwise. |
| `read_served_file` | Raw text content of a single served file (max 10 MB, UTF-8 only — refuses rather than truncates or garbles). |
| `get_asset_metrics` | Raw size, in-memory gzip size, and compression ratio for a file or directory of served assets. No brotli — see Additional read-only tools. |
| `validate_source_maps` | Structural validity of a `.map` file (JSON parses, has `version`/`sources`/`mappings`, a corresponding source file exists), or a `.js`/`.css` file's `sourceMappingURL` comment and the map it references. Does not validate the VLQ mapping data itself. |
| `simulate_request` | Status, headers, and a body preview for a path, evaluated in-process (no real network call). `path` + optional `method` (`GET`/`HEAD` only) — no custom headers/cookies/User-Agent. |
| `inspect_response_headers` | Response headers for a path, optionally filtered to one category (cache/cors/security/cookies/encoding). |
| `scan_for_secrets` | Heuristic scan of served text files for credential-shaped patterns. Output is fully redacted — file/line/rule only. |

## Browser instrumentation
Opt-in via `-mcp-browser` (which requires `-mcp` also be set — the server `log.Fatalf`s at startup otherwise, failing closed on misconfiguration since this flag changes every byte of HTML served, a materially bigger behavior change than the read-only tools above).

### Transport: POST-and-buffer, not WebSocket
The RFC proposed a WebSocket-based `read_live_browser_errors` ("the Holy Grail") streaming stack traces to the agent in real time. What's built instead: `get_console_log` is pulled on demand, the same way `get_recent_requests` already is — nothing here needs server-initiated push. A WebSocket is straightforward with stdlib `net/http` hijacking in principle, but hand-rolling framing, ping/pong, and concurrent-writer safety correctly is materially more code and state than this debug tool needs. So, consistent with the rest of this server's hand-rolled JSON-RPC-over-HTTP approach and the zero-dependency constraint, two more reserved sub-paths were added under the existing `/__mcp` prefix:
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
Six tools beyond the original three-tool prototype, all in `src/server/`, all under `-mcp` alone (no new flag — none mutate the filesystem, server state, or make outbound network calls):

- **`read_served_file`** (`files.go`) and **`list_files`** (`mcp.go`) share one traversal-guard implementation, `resolveServedPath` (see Security model for what it does and doesn't catch).
- **`get_asset_metrics`** (`assets.go`) reports raw size and gzip size (`compress/gzip`, stdlib) with a compression ratio, plus a compute-time figure documented explicitly as a proxy for serving cost, not a network transfer-time measurement (localhost transfer time isn't meaningful to measure — the RFC's "server transfer times" wording is not what's actually reported). **No brotli**: Go's standard library has no brotli encoder, and vendoring one would violate the zero-dependency constraint — this is a permanent scope limit, not a TODO.
- **`validate_source_maps`** (`assets.go`) parses `.map` JSON for required fields (`version`, `sources`, `mappings`) and, for `.js`/`.css` input, locates the `sourceMappingURL` comment and validates the map it points to (resolved through `resolveServedPath`, so it can't escape the served root either — modulo the symlink caveat above). This is structural validation only — it does not decode the VLQ `mappings` string to confirm the map actually maps back to correct source positions, which is a materially deeper check than "eliminating silent source map degradation" (the RFC's framing) implies.
- **`simulate_request`** and **`inspect_response_headers`** (`simulate.go`) share `runInternalRequest`, which calls this server's own `ServeHTTP` via `httptest.NewRecorder` — see the Security model's SSRF bullet above for why this can never make a real network call regardless of input, and for the RFC features (custom headers/cookies/User-Agent) that were not implemented. `simulate_request`'s response body is capped at 64 KB.
- **`scan_for_secrets`** (`secrets.go`) is a small, hand-picked set of high-confidence regex rules (AWS access key IDs, PEM private key headers, GitHub/Slack tokens, generic `api_key`/`secret`/`token` assignments) — not an exhaustive secret-scanning product, and a clean result is not proof of absence of secrets. **Output is fully redacted**: results contain only `file`, `line`, and `rule` — the matched text itself is never included in the response, logged, or otherwise surfaced. This was a deliberate design decision: a tool whose purpose is finding secrets must not itself become a channel that pipes live credentials into an agent's context. Files over 2 MB and known binary/media extensions are skipped (wasted work, not a security gap — hand-typed secrets don't end up in compiled binaries). Results capped at 200 matches.

## Excluded by design
These tools/behaviors from the original `MCP-server-specs.md` RFC were deliberately **not** built, as a permanent decision rather than deferred work:
- **`mock_api_route`** (fake response bodies/status codes) and **`throttle_asset`** (injected latency/packet loss) both mutate server behavior, unlike every other tool here. ZippyServe's MCP server stays strictly read-only — no tool changes what gets served or how, even in-memory-only and even reset-on-restart. This is a deliberate, permanent line, not a gap to fill later.
- **`list_web_root`** and **`get_recent_logs`** from the RFC are already satisfied by `list_files` and `get_recent_requests` respectively — no duplicate tools were added.
- **`read_live_browser_errors`** (proposed as WebSocket-based real-time streaming) is satisfied by `get_console_log` — pull-based rather than push; see Browser instrumentation.
- **OS-level monitoring** (`get_cpu_info`, `get_memory_info`, `get_disk_usage`, `get_network_interfaces`) — proposed and then excluded by the RFC itself: standard OS utilities already handle host metrics, and forwarding them to an LLM's context provides no web-app debugging value. Preserved here since this document now supersedes that RFC.
- **stdio as the primary transport** — the RFC's stated preference; not used, for the reasons in Transport above.

## Open questions for follow-up
- Should the MCP endpoint's bind extend to non-localhost use (e.g. serving to a container/VM where the agent runs elsewhere)? **Closed: no.** ZippyServe's MCP server will never bind beyond `127.0.0.1`. An agent that needs to reach a containerized ZippyServe instance should run in the same container/network namespace and talk to it via `localhost` — not request network exposure of the port.
- **Should `POST /__mcp` validate `Origin` the same way `/__mcp/report` does?** Given the confused-deputy risk documented in Security model above, this looks like the right default rather than an edge case — tracked here as the leading candidate for a near-term follow-up rather than closed silently.
- Should `resolveServedPath` (or file serving generally) resolve symlinks/junctions before the prefix check, to close the gap confirmed in Security model? Would need a decision on whether following symlinks inside the served root should ever be legal (some dev setups rely on it, e.g. symlinked shared asset directories) or rejected outright.
- Should `get_recent_requests` support filtering (by status code, path prefix) once real usage shows what agents actually need?
- Should the injected script also wrap `fetch`/`XMLHttpRequest` to capture client-observed failed network requests (timeouts, CORS failures, non-2xx responses the page's own JS sees)? Deliberately out of scope for now — `get_recent_requests` already covers server-observed outcomes, and this would add a second interception surface with its own edge cases (aborted requests, streamed bodies). Revisit if real usage shows a gap.
- Should ZippyServe detect and surface (e.g. at startup, or via `get_server_info`) when a served page's own CSP would silently block the injected script, rather than leaving it as an undetectable failure mode? Would likely require parsing `<meta http-equiv="Content-Security-Policy">` tags in served HTML — not attempted here.
