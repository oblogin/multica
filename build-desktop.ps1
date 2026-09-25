$ErrorActionPreference = 'Stop'

$desktopRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'apps\desktop'))
$distRoot = Join-Path $desktopRoot 'dist'
$sha = [System.Security.Cryptography.SHA256]::Create()
try {
    $pathBytes = [System.Text.Encoding]::UTF8.GetBytes($desktopRoot.ToLowerInvariant())
    $pathHash = [System.BitConverter]::ToString($sha.ComputeHash($pathBytes)).Replace('-', '')
} finally {
    $sha.Dispose()
}

# Builds in one checkout share out/renderer, so keep the lock through packaging.
$mutex = [System.Threading.Mutex]::new($false, "Local\MulticaDesktopBuild-$pathHash")
$ownsMutex = $false
$exitCode = 1

try {
    try {
        $ownsMutex = $mutex.WaitOne(0)
    } catch [System.Threading.AbandonedMutexException] {
        $ownsMutex = $true
    }

    if (-not $ownsMutex) {
        [Console]::Error.WriteLine("A desktop build is already running in $desktopRoot.")
        $exitCode = 2
    } else {
        Push-Location -LiteralPath $desktopRoot
        try {
            $env:CSC_IDENTITY_AUTO_DISCOVERY = 'false'
            & pnpm.cmd run package -- --win --x64 --publish never --config.win.signAndEditExecutable=false
            $exitCode = $LASTEXITCODE

            if ($exitCode -eq 0) {
                $installer = Get-ChildItem -LiteralPath $distRoot -Filter 'multica-desktop-*-windows-x64.exe' -File |
                    Select-Object -First 1
                if ($null -eq $installer) {
                    [Console]::Error.WriteLine("The Windows installer was not found in $distRoot.")
                    $exitCode = 1
                } else {
                    Start-Process -FilePath explorer.exe -ArgumentList ('"{0}"' -f $distRoot)
                }
            }
        } finally {
            Pop-Location
        }
    }
} catch {
    [Console]::Error.WriteLine("Desktop build failed: $($_.Exception.Message)")
    $exitCode = 1
} finally {
    if ($ownsMutex) {
        $mutex.ReleaseMutex()
    }
    $mutex.Dispose()
}

exit $exitCode
