// This file is part of ZippyServe
// mcp.go
// Author(s): Gabriel Mongefranco.
// Created: 2026-08-02
// Summary: Optional built-in MCP (Model Context Protocol) server, enabled with
//          -mcp, that exposes read-only tools so AI coding agents can inspect a
//          running ZippyServe instance (server info, served files, recent request
//          log) without a browser plug-in. Prototype scope only — see
//          docs/mcp-design.md for the full design and deferred future work.
// Notes: See README file for documentation and full license information.
//
// Copyright © 2026 The Regents of the University of Michigan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or (at your option) any later version.
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
// You should have received a copy of the GNU General Public License along
// with this program. If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// mcpPathPrefix is the reserved URL path the built-in MCP server listens on.
	// Requests under this prefix never fall through to file serving, regardless
	// of whether -mcp is enabled, so a served file can never collide with it.
	mcpPathPrefix = "/__mcp"

	// mcpProtocolVersion is the MCP spec revision this hand-rolled server speaks.
	// See https://modelcontextprotocol.io/specification
	mcpProtocolVersion = "2025-06-18"

	// maxRequestLogEntries bounds the in-memory request-log ring buffer used by
	// the get_recent_requests tool.
	maxRequestLogEntries = 200

	// maxListFilesResults caps list_files output so a large served tree can't
	// produce an unbounded response.
	maxListFilesResults = 500
)

// requestLogEntry records one served HTTP request for the get_recent_requests
// MCP tool.
type requestLogEntry struct {
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS float64   `json:"durationMs"`
	Time       time.Time `json:"time"`
}

var (
	requestLog   []requestLogEntry
	requestLogMu sync.Mutex
)

// recordRequest appends an entry to the in-memory request log, evicting the
// oldest entry once maxRequestLogEntries is reached. Safe for concurrent use.
func recordRequest(entry requestLogEntry) {
	requestLogMu.Lock()
	defer requestLogMu.Unlock()
	requestLog = append(requestLog, entry)
	if len(requestLog) > maxRequestLogEntries {
		requestLog = requestLog[len(requestLog)-maxRequestLogEntries:]
	}
}

// recentRequests returns up to limit of the most recent request-log entries,
// newest first. Safe for concurrent use.
func recentRequests(limit int) []requestLogEntry {
	requestLogMu.Lock()
	defer requestLogMu.Unlock()
	if limit <= 0 || limit > len(requestLog) {
		limit = len(requestLog)
	}
	result := make([]requestLogEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = requestLog[len(requestLog)-1-i]
	}
	return result
}

// statusRecorder wraps http.ResponseWriter to capture the status code written,
// so ServeHTTP can feed it to the request log without changing every handler.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if !sr.wroteHeader {
		sr.status = code
		sr.wroteHeader = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

// --- JSON-RPC 2.0 plumbing (hand-rolled to preserve the project's
// zero-dependency policy; see docs/mcp-design.md) ---

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	jsonRPCParseError     = -32700
	jsonRPCMethodNotFound = -32601
	jsonRPCInvalidParams  = -32602
	jsonRPCInternalError  = -32603
)

// mcpTool describes one MCP tool for the tools/list response, following the
// MCP spec's Tool shape (name, description, inputSchema as JSON Schema).
type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

var mcpTools = []mcpTool{
	{
		Name:        "get_server_info",
		Description: "Get this ZippyServe instance's port, serving root, index file, version, and uptime.",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
	},
	{
		Name:        "list_files",
		Description: "List files under the served root (or a subdirectory of it), with size and last-modified time.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Subdirectory of the served root to list, relative, e.g. \"assets\". Defaults to the root itself.",
				},
			},
		},
	},
	{
		Name:        "get_recent_requests",
		Description: "Get the most recent HTTP requests this server has handled (method, path, status, duration), newest first.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of entries to return (default 50, max 200).",
				},
			},
		},
	},
}

// handleMCPRequest is the HTTP entry point for the built-in MCP server, mounted
// at mcpPathPrefix. It implements the minimal JSON-RPC 2.0 method set an MCP
// client needs for a read-only, tools-only server: initialize, notifications
// (ignored), tools/list, and tools/call.
func handleMCPRequest(w http.ResponseWriter, r *http.Request, h *ZippyHandler) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB request cap
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONRPC(w, jsonRPCResponse{JSONRPC: "2.0", Error: &jsonRPCError{Code: jsonRPCParseError, Message: "invalid JSON"}})
		return
	}

	// Notifications (no id) never get a JSON-RPC response body, per spec.
	isNotification := len(req.ID) == 0
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": ServerName, "version": Version},
		}
	case "notifications/initialized":
		isNotification = true
	case "tools/list":
		resp.Result = map[string]interface{}{"tools": mcpTools}
	case "tools/call":
		result, callErr := handleToolCall(h, req.Params)
		if callErr != nil {
			resp.Result = map[string]interface{}{
				"isError": true,
				"content": []map[string]string{{"type": "text", "text": callErr.Error()}},
			}
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &jsonRPCError{Code: jsonRPCMethodNotFound, Message: "method not found: " + req.Method}
	}

	if isNotification {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSONRPC(w, resp)
}

func writeJSONRPC(w http.ResponseWriter, resp jsonRPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleToolCall dispatches a tools/call request to the named tool and wraps
// its result in the MCP "content" shape (a list of text/... blocks).
func handleToolCall(h *ZippyHandler, params json.RawMessage) (map[string]interface{}, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, errInvalidToolParams
	}

	var (
		payload interface{}
		err     error
	)
	switch call.Name {
	case "get_server_info":
		payload = toolGetServerInfo(h)
	case "list_files":
		payload, err = toolListFiles(h, call.Arguments)
	case "get_recent_requests":
		payload, err = toolGetRecentRequests(call.Arguments)
	default:
		return nil, errUnknownTool
	}
	if err != nil {
		return nil, err
	}

	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"content": []map[string]string{{"type": "text", "text": string(text)}},
	}, nil
}

var (
	errInvalidToolParams = simpleError("invalid tool arguments")
	errUnknownTool       = simpleError("unknown tool")
)

type simpleError string

func (e simpleError) Error() string { return string(e) }

func toolGetServerInfo(h *ZippyHandler) map[string]interface{} {
	return map[string]interface{}{
		"name":        ServerName,
		"version":     Version,
		"port":        *portFlag,
		"servingRoot": h.Root,
		"indexFile":   h.Index,
		"uptime":      time.Since(serverStartTime).String(),
	}
}

func toolListFiles(h *ZippyHandler, argsJSON json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Path string `json:"path"`
	}
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return nil, errInvalidToolParams
		}
	}

	// Reuse the same traversal guard as file serving: reject ".." / NUL, and
	// verify the resolved path stays inside the served root.
	cleanRel := filepath.Clean("/" + args.Path)
	if strings.Contains(cleanRel, "..") || strings.Contains(cleanRel, "\x00") {
		return nil, errInvalidToolParams
	}
	base := filepath.Clean(filepath.Join(h.Root, cleanRel))
	if !strings.HasPrefix(base, filepath.Clean(h.Root)) {
		return nil, errInvalidToolParams
	}

	type fileEntry struct {
		Path      string `json:"path"`
		SizeBytes int64  `json:"sizeBytes"`
		ModTime   string `json:"modTime"`
	}
	var entries []fileEntry
	truncated := false

	walkErr := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if len(entries) >= maxListFilesResults {
			truncated = true
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(h.Root, p)
		if relErr != nil {
			return relErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		entries = append(entries, fileEntry{
			Path:      filepath.ToSlash(rel),
			SizeBytes: info.Size(),
			ModTime:   info.ModTime().UTC().Format(time.RFC3339),
		})
		return nil
	})
	if walkErr != nil {
		return nil, simpleError("could not list files: " + walkErr.Error())
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	return map[string]interface{}{
		"root":      h.Root,
		"files":     entries,
		"truncated": truncated,
	}, nil
}

func toolGetRecentRequests(argsJSON json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Limit int `json:"limit"`
	}
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return nil, errInvalidToolParams
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > maxRequestLogEntries {
		limit = maxRequestLogEntries
	}
	return map[string]interface{}{
		"requests": recentRequests(limit),
	}, nil
}
