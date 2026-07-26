# This file is part of ZippyServe
# run-windows.ps1
# Author(s): Gabriel Mongefranco; Eisenberg Family Depression Center.
# Created: 2026-07-26
# Summary: Windows launch script for ZippyServe. Wraps bin\ZippyServe.exe, serving
#          this script's own directory by default, and opens the default browser.
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

param(
    [int]$Port = 8010,
    [string]$Dir = $PSScriptRoot,
    [string]$Zip = "",
    [string]$Compressed = "",
    [string]$Tar = "",
    [string]$Gz = "",
    [string]$Index = ""
)

$ExePath = Join-Path $PSScriptRoot "bin\ZippyServe.exe"

if (-not (Test-Path $ExePath)) {
    Write-Error "ZippyServe binary not found at '$ExePath'. Run build.ps1 first, or copy bin\ZippyServe.exe alongside this script."
    exit 1
}

### Build server argument list ###
# -dir defaults to this script's own directory (not the exe's directory) so the
# copied-in project root is served, not the bin\ folder the exe lives in. main.go's
# own priority order (Index > Zip/Tar/Gz > Dir) still applies, so passing -dir here
# alongside a -Zip/-Index override is safe and does not conflict.
$ServerArgs = @("-port", $Port, "-dir", $Dir)
if ($Zip -ne "")        { $ServerArgs += @("-zip", $Zip) }
if ($Compressed -ne "") { $ServerArgs += @("-compressed", $Compressed) }
if ($Tar -ne "")         { $ServerArgs += @("-tar", $Tar) }
if ($Gz -ne "")          { $ServerArgs += @("-gz", $Gz) }
if ($Index -ne "")       { $ServerArgs += @("-index", $Index) }

Start-Process "http://localhost:$Port"

& $ExePath @ServerArgs
exit $LASTEXITCODE
