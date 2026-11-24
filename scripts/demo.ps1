# Runs trashbox on made-up mail, for screenshots and trying the window out.
# Nothing real shows: TRASHBOX_DEMO swaps the accounts for invented ones
# (engine/internal/demo) and TRASHBOX_DATA gives it a throwaway data folder.
#
#   powershell -File scripts\demo.ps1                     just open it
#   powershell -File scripts\demo.ps1 -Shots docs         retake the README screenshots
#
# -Steps runs test steps instead (see testStep in src/app.cpp), e.g.
#   -Steps "wait 3;select 4;waitload;shot C:\tmp\a.png;quit"
param(
    [string]$Steps = "",
    [string]$Shots = "",
    [string]$Pos = "100,100,1500,940",
    [string]$Exe = "",
    [switch]$Code
)
$ErrorActionPreference = 'Stop'
if (-not $Exe) { $Exe = Join-Path (Split-Path $PSScriptRoot) 'build\trashbox.exe' }
$data = Join-Path $env:TEMP 'trashbox-demo'
Remove-Item -Recurse -Force $data -ErrorAction SilentlyContinue

function Run($steps, [switch]$code) {
    $env:TRASHBOX_DEMO = '1'
    $env:TRASHBOX_DATA = $data
    $env:TRASHBOX_STEPS = $steps
    $env:TRASHBOX_DEMO_CODE = if ($code) { '1' } else { '' }
    $p = Start-Process $Exe -ArgumentList "-place $Pos" -PassThru
    if ($steps) { $p.WaitForExit() }
}

if ($Shots) {
    $out = (Resolve-Path $Shots).Path
    # Rows in All inboxes, newest first: 4 the night train newsletter,
    # 6 the allotment one (pictures blocked), 11 a plain-text reply.
    Run "wait 3;select 4;waitload;wait 0.8;shot $out\main.png;select 11;waitload;wait 0.5;shot $out\plain.png;select 6;waitload;wait 0.5;shot $out\blocked.png;quit"
    Remove-Item -Recurse -Force $data -ErrorAction SilentlyContinue
    # A sign-in code email arrives 3 s in.
    Run "wait 5;select 5;waitload;wait 0.5;shot $out\code.png;quit" -code
} else {
    Run $Steps -code:$Code
}
