$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$project = Join-Path $root 'azure-relay/TunnelDesktop.Relay.csproj'
$publish = Join-Path $root 'dist/azure-relay/publish'
$zip = Join-Path $root 'dist/azure-relay/tunneldesktop-azure-relay.zip'

Push-Location $root
try {
    dotnet publish $project -c Release -o $publish
    if ($LASTEXITCODE -ne 0) { throw 'dotnet publish failed' }

    if (Test-Path -LiteralPath $zip) {
        Remove-Item -LiteralPath $zip -Force
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $zip) | Out-Null
    Compress-Archive -Path (Join-Path $publish '*') -DestinationPath $zip -Force
    Write-Host "built $zip"
}
finally {
    Pop-Location
}
