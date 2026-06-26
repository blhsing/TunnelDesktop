$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$android = Join-Path $root 'android'

function Use-GradleProxyFromEnv {
    $proxy = $env:HTTPS_PROXY
    if (-not $proxy) { $proxy = $env:HTTP_PROXY }
    if (-not $proxy) { return }

    $uri = [Uri]$proxy
    if (-not $uri.Host) { return }

    $port = $uri.Port
    if ($port -le 0) { $port = 80 }
    $opts = @(
        "-Dhttp.proxyHost=$($uri.Host)",
        "-Dhttp.proxyPort=$port",
        "-Dhttps.proxyHost=$($uri.Host)",
        "-Dhttps.proxyPort=$port"
    )
    if ($env:GRADLE_OPTS) {
        $env:GRADLE_OPTS = "$env:GRADLE_OPTS $($opts -join ' ')"
    }
    else {
        $env:GRADLE_OPTS = $opts -join ' '
    }
}

Use-GradleProxyFromEnv

Push-Location $android
try {
    if (Test-Path '.\gradlew.bat') {
        .\gradlew.bat assembleDebug
        if ($LASTEXITCODE -ne 0) { throw 'Gradle build failed' }
    }
    else {
        gradle assembleDebug
        if ($LASTEXITCODE -ne 0) { throw 'Gradle build failed' }
    }
}
finally {
    Pop-Location
}
