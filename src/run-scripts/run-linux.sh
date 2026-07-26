#!/bin/bash
# This file is part of ZippyServe
# run-linux.sh
# Author(s): Gabriel Mongefranco; Eisenberg Family Depression Center.
# Created: 2026-07-26
# Summary: Linux launch script for ZippyServe.
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

xdg-open "http://localhost:8010" &

if command -v python3 &> /dev/null; then
    python3 run-python.py
elif [ -f "./zippyserve" ]; then
    ./zippyserve
else
    echo "No fallback environments found."
fi