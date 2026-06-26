$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$out = Join-Path $root 'dist/bin'
$tmp = Join-Path $root 'dist/tmp'
$goCache = Join-Path $root 'dist/gocache'
New-Item -ItemType Directory -Force -Path $out | Out-Null
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
New-Item -ItemType Directory -Force -Path $goCache | Out-Null

$savedEnv = @{
    GOTMPDIR = $env:GOTMPDIR
    GOCACHE = $env:GOCACHE
    TEMP = $env:TEMP
    TMP = $env:TMP
}
$env:GOTMPDIR = $tmp
$env:GOCACHE = $goCache
$env:TEMP = $tmp
$env:TMP = $tmp

function Restore-EnvValue {
    param(
        [string] $Name,
        [string] $Value
    )
    if ($null -eq $Value) {
        Remove-Item "Env:\$Name" -ErrorAction SilentlyContinue
    }
    else {
        Set-Item "Env:\$Name" $Value
    }
}

function Ensure-Rsrc {
    if (Get-Command rsrc -ErrorAction SilentlyContinue) {
        return
    }
    $goBin = 'D:\Go\bin'
    New-Item -ItemType Directory -Force -Path $goBin | Out-Null
    $oldGoBin = $env:GOBIN
    try {
        $env:GOBIN = $goBin
        go install github.com/akavel/rsrc@latest
        if ($LASTEXITCODE -ne 0) { throw 'go install rsrc failed' }
    }
    finally {
        if ($null -eq $oldGoBin) {
            Remove-Item Env:\GOBIN -ErrorAction SilentlyContinue
        }
        else {
            $env:GOBIN = $oldGoBin
        }
    }
    $env:PATH = "$goBin;$env:PATH"
}

function Build-WindowsResources {
    Ensure-Rsrc
    $resources = @(
        @{ Manifest = 'cmd/agent-configurator/app.manifest'; Output = 'cmd/agent-configurator/rsrc_windows_amd64.syso'; Name = 'agent configurator' },
        @{ Manifest = 'cmd/client/app.manifest'; Output = 'cmd/client/rsrc_windows_amd64.syso'; Name = 'home app' }
    )
    foreach ($resource in $resources) {
        $manifest = Join-Path $root $resource.Manifest
        $output = Join-Path $root $resource.Output
        rsrc -manifest $manifest -arch amd64 -o $output
        if ($LASTEXITCODE -ne 0) { throw "rsrc failed for $($resource.Name) manifest" }
    }
}

Push-Location $root
try {
    Build-WindowsResources

    go mod download
    if ($LASTEXITCODE -ne 0) { throw 'go mod download failed' }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }

    $targets = @(
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Name = 'agent-windows-amd64.exe'; Package = './cmd/agent' },
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Name = 'agent-configurator-windows-amd64.exe'; Package = './cmd/agent-configurator'; Ldflags = '-s -w -H windowsgui' },
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Name = 'client-windows-amd64.exe'; Package = './cmd/client'; Ldflags = '-s -w -H windowsgui' }
    )

    $legacyArtifactPatterns = @('agent-installer-windows-amd64.exe', 'relay-*')
    foreach ($legacyArtifactPattern in $legacyArtifactPatterns) {
        Get-ChildItem -LiteralPath $out -Filter $legacyArtifactPattern -File -ErrorAction SilentlyContinue | ForEach-Object {
            Remove-Item -LiteralPath $_.FullName -Force
        }
    }

    foreach ($target in $targets) {
        $env:GOOS = $target.GOOS
        $env:GOARCH = $target.GOARCH
        $env:CGO_ENABLED = '0'
        $path = Join-Path $out $target.Name
        $ldflags = '-s -w'
        if ($target.ContainsKey('Ldflags')) {
            $ldflags = $target.Ldflags
        }
        go build -trimpath -ldflags $ldflags -o $path $target.Package
        if ($LASTEXITCODE -ne 0) { throw "go build failed for $($target.Name)" }
        if (-not (Test-Path -LiteralPath $path)) {
            throw "go build did not produce $path. Endpoint protection may have quarantined the generated executable."
        }
        Start-Sleep -Seconds 3
        if (-not (Test-Path -LiteralPath $path)) {
            throw "$path was removed after build. Endpoint protection may have quarantined the generated executable."
        }
        Write-Host "built $path"
    }
}
finally {
    Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
    Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
    Restore-EnvValue -Name 'GOTMPDIR' -Value $savedEnv.GOTMPDIR
    Restore-EnvValue -Name 'GOCACHE' -Value $savedEnv.GOCACHE
    Restore-EnvValue -Name 'TEMP' -Value $savedEnv.TEMP
    Restore-EnvValue -Name 'TMP' -Value $savedEnv.TMP
    Pop-Location
}
