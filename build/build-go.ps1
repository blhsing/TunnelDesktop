$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$out = Join-Path $root 'dist/bin'
New-Item -ItemType Directory -Force -Path $out | Out-Null

Push-Location $root
try {
    go mod download
    if ($LASTEXITCODE -ne 0) { throw 'go mod download failed' }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }

    $targets = @(
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Name = 'agent-windows-amd64.exe'; Package = './cmd/agent' },
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Name = 'agent-installer-windows-amd64.exe'; Package = './cmd/agent-configurator'; Ldflags = '-s -w -H windowsgui' },
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Name = 'agent-configurator-windows-amd64.exe'; Package = './cmd/agent-configurator'; Ldflags = '-s -w -H windowsgui' },
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Name = 'client-windows-amd64.exe'; Package = './cmd/client' },
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Name = 'relay-windows-amd64.exe'; Package = './cmd/relay' },
        @{ GOOS = 'linux'; GOARCH = 'amd64'; Name = 'relay-linux-amd64'; Package = './cmd/relay' },
        @{ GOOS = 'linux'; GOARCH = 'arm64'; Name = 'relay-linux-arm64'; Package = './cmd/relay' }
    )

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
        Write-Host "built $path"
    }
}
finally {
    Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
    Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}
