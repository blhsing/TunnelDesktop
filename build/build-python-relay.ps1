$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$source = Join-Path $root 'relay/python'
$requirements = Join-Path $source 'requirements.txt'
$publish = Join-Path $root 'dist/python-relay/publish'
$zip = Join-Path $root 'dist/python-relay/deskferry-python-relay.zip'
$vendor = Join-Path $root 'dist/python-relay/vendor-linux-cp39'
$vendoredPublish = Join-Path $root 'dist/python-relay/publish-linux-cp39-vendored'
$vendoredZip = Join-Path $root 'dist/python-relay/deskferry-python-relay-linux-cp39-vendored.zip'

Push-Location $root
try {
    python -m pytest relay/python/tests
    if ($LASTEXITCODE -ne 0) { throw 'python relay tests failed' }

    if (Test-Path -LiteralPath $publish) {
        Remove-Item -LiteralPath $publish -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $publish | Out-Null
    Copy-Item -LiteralPath (Join-Path $source 'app.py') -Destination $publish
    Copy-Item -LiteralPath (Join-Path $source 'requirements.txt') -Destination $publish
    Copy-Item -LiteralPath (Join-Path $source 'startup.sh') -Destination $publish

    if (Test-Path -LiteralPath $zip) {
        Remove-Item -LiteralPath $zip -Force
    }
    Get-ChildItem -LiteralPath (Split-Path -Parent $zip) -Filter 'tunneldesktop-python-relay*.zip' -File -ErrorAction SilentlyContinue | ForEach-Object {
        Remove-Item -LiteralPath $_.FullName -Force
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $zip) | Out-Null
    Compress-Archive -Path (Join-Path $publish '*') -DestinationPath $zip -Force
    Write-Host "built $zip"

    if (Test-Path -LiteralPath $vendor) {
        Remove-Item -LiteralPath $vendor -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $vendor | Out-Null
    python -m pip install --target $vendor --platform manylinux2014_x86_64 --implementation cp --python-version 3.9 --abi cp39 --only-binary=:all: -r $requirements
    if ($LASTEXITCODE -ne 0) { throw 'python relay Linux dependency vendoring failed' }
    python -m pip install --target $vendor --platform manylinux2014_x86_64 --implementation cp --python-version 3.9 --abi cp39 --only-binary=:all: 'exceptiongroup>=1.0' 'eval-type-backport>=0.2'
    if ($LASTEXITCODE -ne 0) { throw 'python relay Linux compatibility dependency vendoring failed' }

    if (Test-Path -LiteralPath $vendoredPublish) {
        Remove-Item -LiteralPath $vendoredPublish -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $vendoredPublish | Out-Null
    Copy-Item -LiteralPath (Join-Path $source 'app.py') -Destination $vendoredPublish
    Copy-Item -LiteralPath (Join-Path $source 'requirements.txt') -Destination $vendoredPublish
    Copy-Item -LiteralPath (Join-Path $source 'startup.sh') -Destination $vendoredPublish
    Copy-Item -LiteralPath $vendor -Destination (Join-Path $vendoredPublish 'vendor') -Recurse

    if (Test-Path -LiteralPath $vendoredZip) {
        Remove-Item -LiteralPath $vendoredZip -Force
    }
    Compress-Archive -Path (Join-Path $vendoredPublish '*') -DestinationPath $vendoredZip -Force
    Write-Host "built $vendoredZip"
}
finally {
    Pop-Location
}
