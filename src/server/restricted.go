// This file is part of ZippyServe
// restricted.go
// Author(s): PHJ2000.
// Created: 2026-10-29
// Summary: Limits local application serving to named files and the loopback origin.
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
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// fileAllowlist holds exact URL paths for regular files in restricted mode.
type fileAllowlist map[string]struct{}

func (files *fileAllowlist) String() string {
	return "root-relative file paths"
}

func (files *fileAllowlist) Set(name string) error {
	if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") {
		return errors.New("expected a root-relative file path separated by slashes")
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return errors.New("hidden files are not allowed in restricted mode")
		}
	}
	if *files == nil {
		*files = make(fileAllowlist)
	}
	(*files)["/"+name] = struct{}{}
	return nil
}

func (h *ZippyHandler) serveAllowedFile(w http.ResponseWriter, r *http.Request) {
	origins := r.Header.Values("Origin")
	if r.Host != h.LocalAuthority || (len(origins) != 0 &&
		(len(origins) != 1 || origins[0] != "http://"+h.LocalAuthority)) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := h.AllowedFiles[r.URL.Path]; !ok {
		http.NotFound(w, r)
		return
	}
	file, err := h.openVisible(strings.TrimPrefix(r.URL.Path, "/"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path.Base(r.URL.Path), info.ModTime(), file)
}
