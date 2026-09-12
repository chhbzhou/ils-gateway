param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    [string]$Output = "$PSScriptRoot\..\bin\locspoofd-$Arch"
)

$ErrorActionPreference = 'Stop'
$go = if ($env:GO) { $env:GO } else { 'go' }
$null = Get-Command $go -ErrorAction Stop
$root = Split-Path -Parent $PSScriptRoot
$outDir = Split-Path -Parent $Output
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

$env:GOOS = 'linux'
$env:GOARCH = $Arch
$env:CGO_ENABLED = '0'
Push-Location $root
try {
    & $go build -trimpath -buildvcs=false -ldflags='-s -w' -o $Output "$root\cmd\locspoofd"
    if ($LASTEXITCODE -ne 0) { throw "Go build failed: $LASTEXITCODE" }
} finally {
    Pop-Location
}
Write-Output $Output
