$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$aar = Join-Path $root 'android/app/libs/relaycore.aar'

if (-not (Get-Command gomobile -ErrorAction SilentlyContinue)) {
    go install golang.org/x/mobile/cmd/gomobile@latest
    if ($LASTEXITCODE -ne 0) { throw 'go install gomobile failed' }
    $goPath = (& go env GOPATH).Trim()
    $env:PATH = "$goPath\bin;$env:PATH"
}

if (-not $env:ANDROID_HOME -and -not $env:ANDROID_SDK_ROOT) {
    throw 'ANDROID_HOME or ANDROID_SDK_ROOT must point to an Android SDK before building the AAR.'
}

Push-Location $root
try {
    gomobile init
    if ($LASTEXITCODE -ne 0) { throw 'gomobile init failed' }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $aar) | Out-Null
    gomobile bind -target 'android/arm64,android/arm' -androidapi 24 -o $aar ./mobile/relaycore
    if ($LASTEXITCODE -ne 0) { throw 'gomobile bind failed' }
    Write-Host "built $aar"
}
finally {
    Pop-Location
}
