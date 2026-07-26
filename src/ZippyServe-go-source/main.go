// This file is part of ZippyServe
// main.go
// Author(s): Gabriel Mongefranco.
// Created: 2026-07-26
// Summary: Go compiled binary serving static files securely. Main entry point.
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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	port := "8010"
	dir := "."

	// Prevent path traversal securely
	fs := http.FileServer(http.Dir(dir))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cleanPath := filepath.Clean(r.URL.Path)
		if strings.Contains(cleanPath, "..") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		
		// TODO: Add strict zip extraction and markdown rendering logic here
		fs.ServeHTTP(w, r)
	})

	fmt.Printf("Starting ZippyServe fallback on http://localhost:%s\n", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Server failed: %v\n", err)
		os.Exit(1)
	}
}