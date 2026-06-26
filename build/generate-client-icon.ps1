param(
    [string] $IcoPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'cmd/client/app.ico'),
    [string] $PreviewPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'cmd/client/app-icon-256.png')
)

$ErrorActionPreference = 'Stop'

Add-Type -AssemblyName System.Drawing

function New-RectF {
    param([double] $X, [double] $Y, [double] $Width, [double] $Height)
    return New-Object System.Drawing.RectangleF -ArgumentList @([single] $X, [single] $Y, [single] $Width, [single] $Height)
}

function New-PointF {
    param([double] $X, [double] $Y)
    return New-Object System.Drawing.PointF -ArgumentList @([single] $X, [single] $Y)
}

function New-RoundedRectPath {
    param([double] $X, [double] $Y, [double] $Width, [double] $Height, [double] $Radius)

    $path = New-Object System.Drawing.Drawing2D.GraphicsPath
    $diameter = [single] ($Radius * 2)
    $path.AddArc([single] $X, [single] $Y, $diameter, $diameter, 180, 90)
    $path.AddArc([single] ($X + $Width - $diameter), [single] $Y, $diameter, $diameter, 270, 90)
    $path.AddArc([single] ($X + $Width - $diameter), [single] ($Y + $Height - $diameter), $diameter, $diameter, 0, 90)
    $path.AddArc([single] $X, [single] ($Y + $Height - $diameter), $diameter, $diameter, 90, 90)
    $path.CloseFigure()
    return $path
}

function New-Argb {
    param([int] $Alpha, [int] $Red, [int] $Green, [int] $Blue)
    return [System.Drawing.Color]::FromArgb($Alpha, $Red, $Green, $Blue)
}

function Render-ClientIcon {
    param([int] $Size)

    $bitmap = New-Object System.Drawing.Bitmap -ArgumentList @($Size, $Size, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $graphics.Clear([System.Drawing.Color]::Transparent)

    $scale = [double] $Size
    $outer = New-RoundedRectPath ($scale * 0.055) ($scale * 0.055) ($scale * 0.89) ($scale * 0.89) ($scale * 0.20)
    $bgRect = New-RectF ($scale * 0.055) ($scale * 0.055) ($scale * 0.89) ($scale * 0.89)
    $bgBrush = New-Object System.Drawing.Drawing2D.LinearGradientBrush -ArgumentList @(
        $bgRect,
        (New-Argb 255 22 92 170),
        (New-Argb 255 17 177 156),
        [System.Drawing.Drawing2D.LinearGradientMode]::ForwardDiagonal
    )
    $graphics.FillPath($bgBrush, $outer)

    $glowPen = New-Object System.Drawing.Pen -ArgumentList @((New-Argb 90 255 255 255), [single] ([Math]::Max(1.0, $scale * 0.018)))
    $graphics.DrawPath($glowPen, $outer)

    $shadowBrush = New-Object System.Drawing.SolidBrush -ArgumentList @((New-Argb 46 0 28 55))
    $homeBrush = New-Object System.Drawing.SolidBrush -ArgumentList @((New-Argb 250 255 255 255))
    $doorBrush = New-Object System.Drawing.SolidBrush -ArgumentList @((New-Argb 235 17 92 170))
    $accentBrush = New-Object System.Drawing.SolidBrush -ArgumentList @((New-Argb 255 255 190 75))

    $roofShadow = [System.Drawing.PointF[]] @(
        (New-PointF ($scale * 0.22) ($scale * 0.54)),
        (New-PointF ($scale * 0.50) ($scale * 0.29)),
        (New-PointF ($scale * 0.78) ($scale * 0.54)),
        (New-PointF ($scale * 0.70) ($scale * 0.54)),
        (New-PointF ($scale * 0.50) ($scale * 0.37)),
        (New-PointF ($scale * 0.30) ($scale * 0.54))
    )
    $graphics.TranslateTransform(0, [single] ([Math]::Max(1.0, $scale * 0.018)))
    $graphics.FillPolygon($shadowBrush, $roofShadow)
    $graphics.ResetTransform()

    $bodyShadow = New-RoundedRectPath ($scale * 0.30) ($scale * 0.52) ($scale * 0.40) ($scale * 0.27) ($scale * 0.045)
    $graphics.TranslateTransform(0, [single] ([Math]::Max(1.0, $scale * 0.018)))
    $graphics.FillPath($shadowBrush, $bodyShadow)
    $graphics.ResetTransform()
    $bodyShadow.Dispose()

    $graphics.FillPolygon($homeBrush, $roofShadow)
    $body = New-RoundedRectPath ($scale * 0.30) ($scale * 0.50) ($scale * 0.40) ($scale * 0.28) ($scale * 0.045)
    $graphics.FillPath($homeBrush, $body)

    $door = New-RoundedRectPath ($scale * 0.46) ($scale * 0.60) ($scale * 0.11) ($scale * 0.18) ($scale * 0.022)
    $graphics.FillPath($doorBrush, $door)

    $curve = New-Object System.Drawing.Drawing2D.GraphicsPath
    $curve.AddBezier(
        (New-PointF ($scale * 0.57) ($scale * 0.43)),
        (New-PointF ($scale * 0.65) ($scale * 0.34)),
        (New-PointF ($scale * 0.73) ($scale * 0.27)),
        (New-PointF ($scale * 0.84) ($scale * 0.21))
    )
    $signalPen = New-Object System.Drawing.Pen -ArgumentList @((New-Argb 245 255 190 75), [single] ([Math]::Max(2.0, $scale * 0.045)))
    $signalPen.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
    $signalPen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
    $graphics.DrawPath($signalPen, $curve)

    $innerPen = New-Object System.Drawing.Pen -ArgumentList @((New-Argb 180 210 255 250), [single] ([Math]::Max(1.0, $scale * 0.018)))
    $innerPen.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
    $innerPen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
    $graphics.DrawPath($innerPen, $curve)

    $dot = New-RectF ($scale * 0.79) ($scale * 0.15) ($scale * 0.11) ($scale * 0.11)
    $graphics.FillEllipse($accentBrush, $dot)

    foreach ($item in @($bgBrush, $glowPen, $shadowBrush, $homeBrush, $doorBrush, $accentBrush, $outer, $body, $door, $curve, $signalPen, $innerPen)) {
        if ($null -ne $item) {
            $item.Dispose()
        }
    }
    $graphics.Dispose()
    return $bitmap
}

function Get-PngBytes {
    param([System.Drawing.Bitmap] $Bitmap)
    $stream = New-Object System.IO.MemoryStream
    $Bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
    return $stream.ToArray()
}

function Write-Ico {
    param(
        [string] $Path,
        [object[]] $Images
    )

    $stream = [System.IO.File]::Create($Path)
    $writer = New-Object System.IO.BinaryWriter -ArgumentList @($stream)
    try {
        $writer.Write([uint16] 0)
        $writer.Write([uint16] 1)
        $writer.Write([uint16] $Images.Count)

        $offset = 6 + (16 * $Images.Count)
        foreach ($image in $Images) {
            $sizeByte = if ($image.Size -ge 256) { 0 } else { [byte] $image.Size }
            $writer.Write([byte] $sizeByte)
            $writer.Write([byte] $sizeByte)
            $writer.Write([byte] 0)
            $writer.Write([byte] 0)
            $writer.Write([uint16] 1)
            $writer.Write([uint16] 32)
            $writer.Write([uint32] $image.Bytes.Length)
            $writer.Write([uint32] $offset)
            $offset += $image.Bytes.Length
        }

        foreach ($image in $Images) {
            $writer.Write([byte[]] $image.Bytes)
        }
    }
    finally {
        $writer.Dispose()
        $stream.Dispose()
    }
}

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $IcoPath) | Out-Null
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $PreviewPath) | Out-Null

$sizes = @(16, 24, 32, 48, 64, 128, 256)
$images = @()
foreach ($size in $sizes) {
    $bitmap = Render-ClientIcon -Size $size
    try {
        if ($size -eq 256) {
            $bitmap.Save($PreviewPath, [System.Drawing.Imaging.ImageFormat]::Png)
        }
        $images += [pscustomobject] @{
            Size = $size
            Bytes = Get-PngBytes -Bitmap $bitmap
        }
    }
    finally {
        $bitmap.Dispose()
    }
}

Write-Ico -Path $IcoPath -Images $images
Write-Host "generated $IcoPath"
