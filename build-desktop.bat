@echo off
setlocal

pushd "%~dp0apps\desktop" || exit /b 1
set "CSC_IDENTITY_AUTO_DISCOVERY=false"

call pnpm run package -- --win --x64 --publish never --config.win.signAndEditExecutable=false
if errorlevel 1 goto failed

if not exist "dist\multica-desktop-*-windows-x64.exe" goto missing
start "" explorer.exe "%CD%\dist"
popd
exit /b 0

:missing
echo The Windows installer was not found in apps\desktop\dist.
:failed
echo Desktop build failed.
popd
if not defined CI pause
exit /b 1
