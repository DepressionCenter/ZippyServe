<!--
This file is part of ZippyServe
docs/mcp-design.md
Author(s): Gabriel Mongefranco.
Created: 2026-08-02
Summary: Documentation for ZippyServe's the built-in MCP (Model Context
         Protocol) server for AI coding agents.
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

![Eisenberg Family Depression Center](https://code.depressioncenter.org/images/EFDCLogo_375w.png "depressioncenter.org")

# ZippyServe: Built-in MCP server user guide

## Summary
ZippyServe is a static file server. On top of that, it can optionally run a built-in server that speaks [MCP](https://modelcontextprotocol.io/) (Model Context Protocol), the standard AI coding agents (Claude Code, Cursor, Codex, Kimi Code, etc.) use to call tools. When enabled, an agent can inspect a *running* ZippyServe instance directly — see what files it's serving, read the recent request log, pull captured browser console errors, check response headers, scan for accidentally-committed secrets, and more — without a browser extension and without you manually copy-pasting output back and forth.

It ships disabled. Nothing about it changes how ZippyServe serves files unless you turn it on.

## Enabling it

| Flag | What it does | Gotchas |
|---|---|---|
| `-mcp` | Turns on the MCP server at `/__mcp` and all ten tools except `get_console_log`. | Off by default. Always bound to `127.0.0.1` — there is no setting to expose it on the network. |
| `-mcp-browser` | Also injects a small script into every served HTML page, so console output, uncaught errors, and unhandled promise rejections get captured and exposed via `get_console_log`. | Requires `-mcp` to also be set — ZippyServe refuses to start (`log.Fatalf`) if you pass `-mcp-browser` alone. Rewrites HTML bytes as they're served; see [Browser instrumentation](#browser-instrumentation) for the trade-offs. |

Pass these directly to the binary, or to any of the launch scripts:
```
.\run-windows.ps1 -Mcp -McpBrowser
./run-linux.sh --mcp --mcp-browser
./run-mac.command --mcp --mcp-browser
```
(ZippyServe's other flags — `-port`, `-dir`, `-zip`, `-index`, `-serve-dotfiles`, etc. — are unrelated to MCP and covered in the main README. `-serve-dotfiles` in particular is not an MCP setting: it controls plain file serving too, and every MCP tool that touches the served filesystem follows the same default.)

## Connecting an MCP client
ZippyServe does **not** get spawned by your agent/MCP client, and it doesn't spawn one either — it's an ordinary long-running local HTTP server that you start once (e.g. via `run-windows.ps1`) and keep running while you iterate. There's no auto-discovery: no `.well-known` manifest, no registration file, nothing broadcast on the network. Your MCP client has to be pointed at it by URL, the same way it would attach to any remote MCP server:
```
claude mcp add --transport http zippyserve http://127.0.0.1:8010/__mcp
```
(Replace `8010` with whatever `-port` you're using.)

To make this easy to find, ZippyServe prints the exact command — with the real port already filled in — to the console every time `-mcp` is enabled, along with a prompt you can copy straight into an AI coding agent that doesn't have a `claude mcp add`-style command of its own:
```
[ZippyServe] MCP server enabled at http://127.0.0.1:8010/__mcp (read-only, localhost-only)
[ZippyServe] To connect Claude Code: claude mcp add --transport http zippyserve http://127.0.0.1:8010/__mcp
[ZippyServe] Or paste this prompt into your AI coding agent:
[ZippyServe]   A ZippyServe dev server is running with its MCP server at http://127.0.0.1:8010/__mcp .
[ZippyServe]   Connect to it and use its tools (list_files, get_recent_requests,
[ZippyServe]   read_served_file, scan_for_secrets, etc.) to inspect what it's serving.
```

You can also call it directly with any HTTP client, which is useful for debugging or for a client without built-in MCP support:
```
curl -s -X POST http://127.0.0.1:8010/__mcp -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_server_info","arguments":{}}}'
```
A session typically goes `initialize` → `tools/list` (to discover the schema) → repeated `tools/call`.

**Note:** the server speaks plain JSON-RPC 2.0 over HTTP POST (MCP's "Streamable HTTP" transport) — there's no stdio transport and no server-to-client streaming (no SSE, no WebSocket). If a tool's result would otherwise be huge, it's capped rather than streamed; see each tool's limits below.

**Note:** `/__mcp` and its sub-paths (`/__mcp/report`, `/__mcp/inject.js`) are reserved. They're checked before any file-serving logic, whether or not `-mcp`/`-mcp-browser` are on (you'll get a 404, not your file), so don't put a file at those exact paths expecting it to be servable.

## MCP tools
All ten tools are read-only — none of them can change what ZippyServe serves, touch the filesystem, or make an outbound network call. The first nine work under `-mcp` alone; `get_console_log` additionally needs `-mcp-browser`.

| Tool | Requires | What it does |
|---|---|---|
| `get_server_info` | `-mcp` | Port, serving root, index file, version, uptime. |
| `list_files` | `-mcp` | Flat, alphabetically sorted listing (path, size, mtime) of the served root or a subdirectory. |
| `get_recent_requests` | `-mcp` | Most recent HTTP requests this server has handled (method, path, status, duration), newest first. |
| `get_console_log` | `-mcp` + `-mcp-browser` | Recent browser console output, uncaught errors, and unhandled promise rejections captured from pages this instance served. |
| `read_served_file` | `-mcp` | Raw text content of one served file. |
| `get_asset_metrics` | `-mcp` | Raw size, in-memory gzip size, and compression ratio for a file or a whole directory of served assets. |
| `validate_source_maps` | `-mcp` | Structural check of a `.map` file, or of a `.js`/`.css` file's `sourceMappingURL` and the map it points to. |
| `simulate_request` | `-mcp` | Status, headers, and a body preview for a path, evaluated in-process — no real network round trip. |
| `inspect_response_headers` | `-mcp` | Response headers ZippyServe would send for a path, optionally filtered to one category. |
| `scan_for_secrets` | `-mcp` | Heuristic scan of served text files for credential-shaped patterns (AWS keys, private key headers, GitHub/Slack tokens, generic secret assignments). |

### Gotchas, per tool
- **`get_server_info`** returns the serving root as an **absolute filesystem path**. Harmless on a localhost-only tool, but worth knowing if you're screen-sharing.
- **`list_files`** is a flat list, not a tree. Capped at 500 entries; past that, `truncated: true` is set and results are cut off rather than paginated. Like plain file serving, dotfiles and dot-directories (`.git`, `.github`, `.claude`, etc.) are excluded by default, except `.well-known` — see [README.md](../README.md) and pass `-serve-dotfiles` at server startup to include them.
- **`get_recent_requests`** reads from a 200-entry ring buffer that resets on restart — it's not a persistent log. `limit` (default 50, max 200) is the only filter; there's no filtering by status code or path prefix.
- **`get_console_log`** errors out (rather than returning empty) if `-mcp-browser` wasn't passed at startup. It only sees pages loaded/reloaded *after* the instrumentation script was actually injected — anything already open in a browser tab before you enabled `-mcp-browser` won't be captured until reloaded. Buffer is 500 entries, also reset on restart.
- **`read_served_file`** refuses files over 10 MB and anything that isn't valid UTF-8 text — it will not truncate or garble a binary file, it just errors.
- **`get_asset_metrics`** reports gzip size, not brotli — Go's standard library has no brotli encoder, so this is a permanent limitation, not a bug. The "compute time" figure is how long reading+gzipping took, not a network transfer-time estimate. Capped at 500 files per call.
- **`validate_source_maps`** only checks structure (valid JSON, required fields present, a plausibly-matching source file exists) — it does **not** decode the map's `mappings` data to confirm positions actually line up.
- **`simulate_request`** only supports `GET`/`HEAD`, can't target `/__mcp` itself, and previews at most 64 KB of the response body.
- **`inspect_response_headers`** filter values are `cache`, `cors`, `security`, `cookies`, `encoding`, or omit it for everything.
- **`scan_for_secrets`** is a small, hand-picked set of regex rules — it is **not** an exhaustive secret scanner, and a clean result is not proof nothing's leaked. Results are fully redacted (file/line/rule only — the matched text itself is never returned, logged, or otherwise surfaced), capped at 200 matches, and skip files over 2 MB or with a known binary/media extension. Does not descend into dotfiles/dot-directories by default (see `list_files` above), so it won't scan `.git` history for secrets that were later removed from the working tree — that's outside this tool's scope regardless.

## Browser instrumentation
This is what `-mcp-browser` turns on: a small script gets injected into every HTML/Markdown page ZippyServe serves, which reports back to the server so `get_console_log` has something to return.

**What it captures:** `window.onerror` (message, source, line, column, stack when available), `unhandledrejection`, and wrapped `console.log/info/warn/error/debug` (it still calls through to the real console method, so your own devtools output is unaffected).

**What it does *not* capture:** failed `fetch`/`XMLHttpRequest` calls (timeouts, CORS failures, non-2xx responses seen only by client-side JS). Server-observed outcomes for those same requests still show up in `get_recent_requests`.

**Notes:**
- Injecting into `.html`/`.htm` files means ZippyServe reads and rewrites those files by hand instead of using its normal file-serving path — which means **Range and If-Modified-Since support is lost for `.html`/`.htm` files specifically, but only while `-mcp-browser` is on.** Every other file type is unaffected.
- If a served page has its own `<meta http-equiv="Content-Security-Policy">` tag and that policy's `script-src` doesn't allow `'self'`, the browser will silently drop the injected script — the page still loads and works fine, it's just not instrumented. ZippyServe now logs a one-line warning (once per path) when it detects a page has *any* CSP meta tag, so you have a hint — but it doesn't parse the policy, so the warning can fire even if that policy would actually have allowed the script.
- Reports are sent fire-and-forget (`navigator.sendBeacon`, falling back to `fetch(..., {keepalive:true})`) to `POST /__mcp/report`, and the script is served from `GET /__mcp/inject.js`. Both 404 if `-mcp-browser` is off, even if `-mcp` is on.

## Security considerations
- **Off by default, and always localhost-only.** There is no flag or configuration to bind the MCP endpoint (or ZippyServe itself) to a network interface. If you need an agent running elsewhere (a container, a VM) to reach it, run the agent in the same network namespace and connect via `localhost` — don't expose the port.
- **Strictly read-only.** No tool can change served content, server configuration, or anything on disk.
- **Can't escape the served folder.** Every tool that takes a `path` argument, and plain file serving itself, is contained to the served root by Go's `os.Root` API — even through a symlink or (on Windows) a junction placed inside the tree that points elsewhere. **Gotcha:** on Windows, this containment is strict enough that a directory junction *inside* the served root pointing at another location that's also inside the root is still blocked — junctions are rejected outright, not evaluated by where they actually point.
- **Dotfiles and dot-directories are excluded by default**, for plain HTTP serving and every MCP tool alike — not a containment control (`os.Root` above is what actually prevents escape), but a visibility default: the served root is commonly a project's working directory, and `.git`, `.github`, `.claude`, and similar tool-config directories were never meant to be reachable this way. `.well-known` (RFC 8615) is always served regardless. Pass `-serve-dotfiles` at startup to disable this and serve everything.
- **Can't be used for SSRF.** `simulate_request` and `inspect_response_headers` run entirely in-process against ZippyServe's own request handling — never a real network dial — so their `path` argument can't be turned into an outbound connection no matter what it contains.
- **A malicious webpage can't read your tools' output through your browser.** `POST /__mcp` checks the `Origin` header: if a browser tab sends one (which a real cross-origin `fetch()`/XHR always does), it must match this instance's own origin or the request is rejected with `403`. Legitimate non-browser MCP clients (CLI tools, curl, an agent's HTTP client) never send `Origin` at all and are unaffected.
- **Console-log reports are also origin-checked**, more strictly: `POST /__mcp/report` requires `Origin` to be present and matching, since it's only ever called by the first-party injected script. This stops another origin's JavaScript from poisoning your console log with fabricated events.
- **Secret-scan results never contain the secret.** `scan_for_secrets` returns only the file, line number, and which rule matched — never the matched text — so the tool that looks for leaked credentials can't itself become a way to leak them into an agent's context.
- **No external dependencies.** The whole MCP server is implemented with Go's standard library only, consistent with ZippyServe's zero-dependency build.

## What it intentionally does not do
- **No tool can mutate server behavior.** There's no "mock this response" or "add latency to this asset" tool — every tool here is read-only, permanently, by design.
- **No OS-level host metrics** (CPU, memory, disk, network interfaces) — out of scope; use your OS's normal tools for that.
- **No non-localhost binding** — see Security considerations above.
