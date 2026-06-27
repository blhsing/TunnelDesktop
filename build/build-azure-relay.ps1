$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$project = Join-Path $root 'relay/azure-dotnet/DeskFerry.Relay.csproj'
$publish = Join-Path $root 'dist/azure-relay/publish'
$zip = Join-Path $root 'dist/azure-relay/deskferry-azure-relay.zip'

Push-Location $root
try {
    if (Test-Path -LiteralPath $publish) {
        Remove-Item -LiteralPath $publish -Recurse -Force
    }
    dotnet publish $project -c Release -o $publish
    if ($LASTEXITCODE -ne 0) { throw 'dotnet publish failed' }

    Get-ChildItem -LiteralPath (Split-Path -Parent $zip) -Filter 'tunneldesktop-azure-relay*.zip' -File -ErrorAction SilentlyContinue | ForEach-Object {
        Remove-Item -LiteralPath $_.FullName -Force
    }
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
