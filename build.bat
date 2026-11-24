@echo off
rem Builds build\trashbox.exe and runs the tests. Needs Visual Studio 2022
rem (or its Build Tools) with the C++ workload, and deps\webview2 (the
rem WebView2 SDK's headers and static loader; tools\fetch-webview2.sh gets
rem them). trashbox-engine.exe is built separately with Go (see CLAUDE.md).
setlocal
cd /d "%~dp0"

set "VSWHERE=%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe"
for /f "usebackq delims=" %%i in (`"%VSWHERE%" -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath`) do set "VS=%%i"
if not defined VS (
    echo Visual Studio with C++ tools not found.
    exit /b 1
)
call "%VS%\VC\Auxiliary\Build\vcvars64.bat" >nul || exit /b 1

if not exist build\obj mkdir build\obj
if not exist build\tests mkdir build\tests
if not exist deps\webview2\x64\WebView2LoaderStatic.lib (
    echo deps\webview2 is missing: run tools\fetch-webview2.sh.
    exit /b 1
)
set CFLAGS=/nologo /std:c++17 /O2 /DNDEBUG /EHsc /utf-8 /W3 /MP /DUNICODE /D_UNICODE /DNOMINMAX /DWIN32_LEAN_AND_MEAN /D_CRT_SECURE_NO_WARNINGS /DIMGUI_DISABLE_DEMO_WINDOWS /DIMGUI_USE_WCHAR32 /Isrc /Ithird_party /Ithird_party\imgui /Ithird_party\imgui\backends /Ideps\webview2\include

echo Building tests...
cl %CFLAGS% /Fobuild\tests\ /Fe:build\tests.exe tests\tests.cpp src\util.cpp /link user32.lib gdi32.lib shell32.lib ole32.lib windowscodecs.lib > build\tests.log 2>&1 || (type build\tests.log & exit /b 1)
build\tests.exe || exit /b 1

echo Building trashbox.exe...
rc /nologo /fo build\app.res src\app.rc || exit /b 1
cl %CFLAGS% /Fobuild\obj\ /Fe:build\trashbox.exe src\*.cpp third_party\imgui\*.cpp third_party\imgui\backends\*.cpp build\app.res deps\webview2\x64\WebView2LoaderStatic.lib ^
    /link /SUBSYSTEM:WINDOWS d3d11.lib dxgi.lib d3dcompiler.lib dwmapi.lib windowscodecs.lib version.lib ^
    ole32.lib oleaut32.lib shell32.lib user32.lib gdi32.lib advapi32.lib > build\build.log 2>&1 || (type build\build.log & exit /b 1)
findstr /i /c:"warning" build\build.log
echo Built build\trashbox.exe
