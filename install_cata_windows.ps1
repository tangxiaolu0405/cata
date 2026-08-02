# install_cata_windows.ps1 — 下载 GitHub Release、解压并配置用户 PATH（Windows）
#
# 用法（PowerShell）:
#   irm https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_cata_windows.ps1 | iex
#   $env:CATA_VERSION = "v0.1.9"; .\install_cata_windows.ps1
#
# 环境变量:
#   CATA_REPO        默认 tangxiaolu0405/cata
#   CATA_VERSION     默认 latest（GitHub 最新 release tag）
#   CATA_INSTALL_DIR 默认 %LOCALAPPDATA%\cata\bin

$ErrorActionPreference = "Stop"

$Repo = if ($env:CATA_REPO) { $env:CATA_REPO } else { "tangxiaolu0405/cata" }
$InstallDir = if ($env:CATA_INSTALL_DIR) { $env:CATA_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "cata\bin" }
$Version = $env:CATA_VERSION
$BinName = "cata.exe"
$GatewayBin = "cata-gateway.exe"

function Log([string]$Message) { Write-Host "==> $Message" }

function Resolve-Version {
    if ($Version) { return }
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
    $script:Version = $release.tag_name
    if (-not $Version) { throw "failed to resolve latest release tag" }
}

function Get-ArtifactName {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
    switch ($arch) {
        "x64" { return "cata-windows-amd64.zip" }
        "x86" { return "cata-windows-386.zip" }
        default { throw "unsupported windows arch: $arch (releases: amd64, 386)" }
    }
}

function Install-Cata {
    $artifact = Get-ArtifactName
    $url = "https://github.com/$Repo/releases/download/$Version/$artifact"
    $tmp = Join-Path $env:TEMP ("cata-install-" + [guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null

    try {
        Log "version: $Version"
        Log "artifact: $artifact"
        Log "download: $url"

        $archive = Join-Path $tmp $artifact
        Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing
        Expand-Archive -Path $archive -DestinationPath $tmp -Force

        $src = Join-Path $tmp $BinName
        if (-not (Test-Path $src)) { throw "archive missing $BinName" }
        $srcGw = Join-Path $tmp $GatewayBin
        if (-not (Test-Path $srcGw)) { throw "archive missing $GatewayBin" }

        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        $dst = Join-Path $InstallDir $BinName
        Copy-Item -Path $src -Destination $dst -Force
        Log "installed: $dst"
        $dstGw = Join-Path $InstallDir $GatewayBin
        Copy-Item -Path $srcGw -Destination $dstGw -Force
        Log "installed: $dstGw"
    }
    finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Ensure-Path {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not $userPath) { $userPath = "" }

    $parts = $userPath -split ";" | Where-Object { $_ -and $_.Trim() -ne "" }
    if ($parts -contains $InstallDir) {
        Log "PATH already contains $InstallDir"
        return
    }

    $newPath = if ($parts.Count -gt 0) { ($parts + $InstallDir) -join ";" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    $env:Path = "$env:Path;$InstallDir"
    Log "added to user PATH: $InstallDir"
    Log "open a new terminal if 'cata' is not found in this session"
}

function Maybe-Init {
    $cataHome = if ($env:CATA_HOME) { $env:CATA_HOME } else { Join-Path $env:USERPROFILE ".cata" }
    if (-not (Test-Path $cataHome)) {
        Log "running cata init + initconfig"
        & (Join-Path $InstallDir $BinName) init
        & (Join-Path $InstallDir $BinName) initconfig
    }
}

Resolve-Version
Install-Cata
Ensure-Path
Maybe-Init
Log "done — try: cata chat   (gateway: cata-gateway)"
