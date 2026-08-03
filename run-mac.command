#!/bin/bash
# This file is part of ZippyServe
# run-mac.command
# Author(s): Gabriel Mongefranco; Eisenberg Family Depression Center.
# Created: 2026-07-26
# Summary: macOS launch script for ZippyServe. Wraps the arch-matched
#          bin/ZippyServe-mac-* binary, serving this script's own directory by
#          default, and opens the default browser. Double-clickable in Finder.
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

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

### Pick the binary matching this Mac's CPU architecture ###
ARCH="$(uname -m)"
if [ "$ARCH" = "arm64" ]; then
    EXE_PATH="$SCRIPT_DIR/bin/ZippyServe-mac-arm64"
else
    EXE_PATH="$SCRIPT_DIR/bin/ZippyServe-mac-amd64"
fi

PORT=8010
DIR="$SCRIPT_DIR"
SERVER_ARGS=()

### Parse and forward server flags ###
while [ $# -gt 0 ]; do
    case "$1" in
        --port=*)       PORT="${1#*=}" ;;
        --port)         PORT="$2"; shift ;;
        --dir=*)        DIR="${1#*=}" ;;
        --dir)          DIR="$2"; shift ;;
        --zip=*)        SERVER_ARGS+=("-zip" "${1#*=}") ;;
        --zip)          SERVER_ARGS+=("-zip" "$2"); shift ;;
        --compressed=*) SERVER_ARGS+=("-compressed" "${1#*=}") ;;
        --compressed)   SERVER_ARGS+=("-compressed" "$2"); shift ;;
        --tar=*)        SERVER_ARGS+=("-tar" "${1#*=}") ;;
        --tar)          SERVER_ARGS+=("-tar" "$2"); shift ;;
        --gz=*)         SERVER_ARGS+=("-gz" "${1#*=}") ;;
        --gz)           SERVER_ARGS+=("-gz" "$2"); shift ;;
        --index=*)      SERVER_ARGS+=("-index" "${1#*=}") ;;
        --index)        SERVER_ARGS+=("-index" "$2"); shift ;;
        --mcp)          SERVER_ARGS+=("-mcp") ;;
        --mcp-browser)  SERVER_ARGS+=("-mcp-browser") ;;
        *)              echo "Unknown argument: $1" >&2; exit 1 ;;
    esac
    shift
done

if [ ! -f "$EXE_PATH" ]; then
    echo "ZippyServe binary not found at '$EXE_PATH'. Run build.sh first, or copy bin/ZippyServe-mac-$ARCH alongside this script." >&2
    exit 1
fi
chmod +x "$EXE_PATH"

open "http://localhost:$PORT"

exec "$EXE_PATH" -port "$PORT" -dir "$DIR" "${SERVER_ARGS[@]}"
