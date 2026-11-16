& {
    $ErrorActionPreference = 'Stop'
    # Windows PowerShell 5.1 downloads many times slower while it draws progress.
    $ProgressPreference = 'SilentlyContinue'
    $Repo = 'hyperion-cs/appleseum'
    if ($env:OS -ne 'Windows_NT') { throw 'This installer requires Windows' }
    $Arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    # Windows 11 on ARM64 runs the AMD64 build through emulation.
    if ($Arch -notin 'AMD64', 'ARM64') { throw "Unsupported Windows architecture: $Arch (requires AMD64 or ARM64)" }
    $AppDir = if ($env:APPLESEUM_INSTALL_DIR) { $env:APPLESEUM_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Appleseum' }
    $AppDir = [IO.Path]::GetFullPath($AppDir)
    $Destination = Join-Path $AppDir 'appleseum.exe'
    if (Test-Path -LiteralPath $Destination -PathType Container) {
        throw "The executable path is not a file: $Destination"
    }
    # Installation only replaces the executable. The server migrates its data on startup.
    $Updating = Test-Path -LiteralPath $Destination
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $Release = if ($env:APPLESEUM_VERSION) {
        "https://github.com/$Repo/releases/download/appleseum-v$($env:APPLESEUM_VERSION -replace '^v')"
    } else {
        "https://github.com/$Repo/releases/latest/download"
    }
    $TmpDir = Join-Path ([IO.Path]::GetTempPath()) ('appleseum-install-' + [guid]::NewGuid().ToString('N'))
    $Staged = Join-Path $AppDir ('appleseum-install-' + [guid]::NewGuid().ToString('N') + '.tmp')
    New-Item -ItemType Directory -Path $TmpDir | Out-Null
    try {
        # The checksum manifest also names the archive, so "latest" needs no API request.
        $Sums = Join-Path $TmpDir 'SHA256SUMS'
        Invoke-WebRequest -UseBasicParsing -Uri "$Release/SHA256SUMS" -OutFile $Sums
        $Entry = [regex]::Match([IO.File]::ReadAllText($Sums), '(?m)^([0-9a-f]{64})  ((appleseum-v[0-9A-Za-z.-]+)-windows-amd64\.zip)\r?$')
        if (-not $Entry.Success) { throw 'The release has no archive for windows-amd64' }
        $Archive = $Entry.Groups[2].Value
        $Action = if ($Updating) { 'Updating' } else { 'Installing' }
        Write-Host "$Action $($Entry.Groups[3].Value) (windows-amd64)"

        $Zip = Join-Path $TmpDir $Archive
        Invoke-WebRequest -UseBasicParsing -Uri "$Release/$Archive" -OutFile $Zip
        if ((Get-FileHash -LiteralPath $Zip -Algorithm SHA256).Hash -ne $Entry.Groups[1].Value) {
            throw 'Checksum mismatch; the existing installation was not changed'
        }
        $Extracted = Join-Path $TmpDir 'extracted'
        Expand-Archive -LiteralPath $Zip -DestinationPath $Extracted
        $Executable = Join-Path $Extracted 'appleseum.exe'
        if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) { throw "$Archive contains no appleseum.exe" }
        New-Item -ItemType Directory -Force -Path $AppDir | Out-Null
        Move-Item -LiteralPath $Executable -Destination $Staged
        try {
            if ($Updating) {
                [IO.File]::Replace($Staged, $Destination, [NullString]::Value)
            } else {
                [IO.File]::Move($Staged, $Destination)
            }
        } catch {
            throw "Could not replace appleseum.exe. Stop Appleseum and check the installation directory permissions before retrying. $($_.Exception.Message)"
        }

        # The registry keeps entries such as %USERPROFILE%\bin unexpanded; .NET would expand them.
        $Environment = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        try {
            $UserPath = $Environment.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
            $Entries = $UserPath -split ';' | ForEach-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') }
            if ($Entries -notcontains $AppDir.TrimEnd('\')) {
                $NewPath = if ($UserPath) { "$($UserPath.TrimEnd(';'));$AppDir" } else { $AppDir }
                $Environment.SetValue('Path', $NewPath, 'ExpandString')
                # .NET broadcasts WM_SETTINGCHANGE after any user variable change, so new terminals see PATH.
                [Environment]::SetEnvironmentVariable('APPLESEUM_INSTALLER', $null, 'User')
            }
        } finally {
            $Environment.Close()
        }
        if (($env:Path -split ';' | ForEach-Object { $_.TrimEnd('\') }) -notcontains $AppDir.TrimEnd('\')) {
            $env:Path += ";$AppDir"
        }

        $Action = if ($Updating) { 'Updated' } else { 'Installed' }
        Write-Host "${Action}: $Destination"
        if ($Updating) {
            Write-Host 'Data was not changed. If you used --data, keep the same path when starting appleseum.exe.'
        } else {
            Set-Location -LiteralPath $AppDir
            Write-Host 'Run: ./appleseum.exe'
        }
    } finally {
        Remove-Item -LiteralPath $TmpDir, $Staged -Recurse -Force -ErrorAction SilentlyContinue
    }
}
