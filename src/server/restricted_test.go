// This file is part of ZippyServe
// restricted_test.go
// Author(s): PHJ2000.
// Created: 2026-10-29
// Summary: Tests restricted local application serving with synthetic files.
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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRestrictedFileServing(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"app.html", "other.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var files fileAllowlist
	if err := files.Set("app.html"); err != nil {
		t.Fatal(err)
	}
	h := &ZippyHandler{RootFS: root, AllowedFiles: files, LocalAuthority: "127.0.0.1:8010"}

	for _, tc := range []struct {
		name, path, authority, origin string
		want                          int
	}{
		{"navigation", "/app.html", "127.0.0.1:8010", "", http.StatusOK},
		{"same origin", "/app.html", "127.0.0.1:8010", "http://127.0.0.1:8010", http.StatusOK},
		{"other file", "/other.txt", "127.0.0.1:8010", "", http.StatusNotFound},
		{"directory", "/", "127.0.0.1:8010", "", http.StatusNotFound},
		{"unexpected origin", "/app.html", "127.0.0.1:8010", "https://other.example", http.StatusForbidden},
		{"opaque origin", "/app.html", "127.0.0.1:8010", "null", http.StatusForbidden},
		{"unexpected host", "/app.html", "other.example", "", http.StatusForbidden},
		{"wrong port", "/app.html", "127.0.0.1:8011", "", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.Host = tc.authority
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("status=%d, response headers=%v", w.Code, w.Header())
			}
			if tc.want == http.StatusOK && w.Body.String() != "synthetic file" {
				t.Fatalf("unexpected file contents: %q", w.Body.String())
			}
		})
	}
}
