@echo off
setlocal EnableExtensions

echo webssh installer for Windows (via WSL)
echo.

rem Installing WSL for the first time needs administrator rights; running
rem webssh itself afterwards does not. "net session" only succeeds when the
rem shell is already elevated, which is a reliable admin check that needs no
rem PowerShell of its own.
net session >nul 2>&1
if not "%errorlevel%"=="0" (
    echo This needs administrator rights the first time, to install WSL.
    echo Requesting elevation...
    powershell -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
    exit /b
)

powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/maxx1980/tai/main/get.ps1 | iex"

echo.
pause
