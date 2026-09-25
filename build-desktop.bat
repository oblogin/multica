@echo off
setlocal

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-desktop.ps1"
set "result=%ERRORLEVEL%"
if not "%result%"=="0" (
    if not "%result%"=="2" echo Desktop build failed with exit code %result%.
    if not defined CI pause
)
exit /b %result%
