# This file is part of ZippyServe
# run-python.py
# Author(s): Gabriel Mongefranco; Eisenberg Family Depression Center.
# Created: 2026-07-26
# Summary: Python built-in static server wrapper.
# Notes: See README file for documentation and full license information.
# 
# Copyright © 2026 The Regents of the University of Michigan
# 
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or (at your option) any later version.
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
# GNU General Public License for more details.
# You should have received a copy of the GNU General Public License along
# with this program. If not, see <https://www.gnu.org/licenses/>.

import http.server
import socketserver
import os

PORT = 8010

class SecureHandler(http.server.SimpleHTTPRequestHandler):
    def translate_path(self, path):
        # Prevent traversal
        path = super().translate_path(path)
        if ".." in path:
            return None
        return path

def main():
    with socketserver.TCPServer(("", PORT), SecureHandler) as httpd:
        print(f"ZippyServe Python running on port {PORT}")
        httpd.serve_forever()

if __name__ == "__main__":
    main()