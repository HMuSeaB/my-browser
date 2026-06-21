param(
    [string]$SourceScreenshot = "docs/images/readme-screenshot.png",
    [string]$Backdrop = "docs/images/release/release-backdrop.png",
    [string]$OutputDir = "docs/images/release"
)

$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Drawing

function New-RoundedPath {
    param(
        [System.Drawing.RectangleF]$Rect,
        [float]$Radius
    )

    $path = New-Object System.Drawing.Drawing2D.GraphicsPath
    $diameter = $Radius * 2

    $path.AddArc($Rect.X, $Rect.Y, $diameter, $diameter, 180, 90)
    $path.AddArc($Rect.Right - $diameter, $Rect.Y, $diameter, $diameter, 270, 90)
    $path.AddArc($Rect.Right - $diameter, $Rect.Bottom - $diameter, $diameter, $diameter, 0, 90)
    $path.AddArc($Rect.X, $Rect.Bottom - $diameter, $diameter, $diameter, 90, 90)
    $path.CloseFigure()
    return $path
}

function Fill-RoundedRect {
    param(
        [System.Drawing.Graphics]$Graphics,
        [System.Drawing.Brush]$Brush,
        [System.Drawing.RectangleF]$Rect,
        [float]$Radius
    )

    $path = New-RoundedPath -Rect $Rect -Radius $Radius
    try {
        $Graphics.FillPath($Brush, $path)
    } finally {
        $path.Dispose()
    }
}

function Draw-RoundedRect {
    param(
        [System.Drawing.Graphics]$Graphics,
        [System.Drawing.Pen]$Pen,
        [System.Drawing.RectangleF]$Rect,
        [float]$Radius
    )

    $path = New-RoundedPath -Rect $Rect -Radius $Radius
    try {
        $Graphics.DrawPath($Pen, $path)
    } finally {
        $path.Dispose()
    }
}

function Draw-ShadowPanel {
    param(
        [System.Drawing.Graphics]$Graphics,
        [System.Drawing.RectangleF]$Rect,
        [float]$Radius,
        [int]$Alpha = 32,
        [float]$Offset = 16
    )

    $shadowRect = [System.Drawing.RectangleF]::new(($Rect.X + $Offset), ($Rect.Y + $Offset), $Rect.Width, $Rect.Height)
    $shadowBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb($Alpha, 5, 13, 28))
    try {
        Fill-RoundedRect -Graphics $Graphics -Brush $shadowBrush -Rect $shadowRect -Radius $Radius
    } finally {
        $shadowBrush.Dispose()
    }
}

function Draw-Label {
    param(
        [System.Drawing.Graphics]$Graphics,
        [string]$Text,
        [float]$X,
        [float]$Y
    )

    $font = New-Object System.Drawing.Font("Microsoft YaHei UI", 18, [System.Drawing.FontStyle]::Regular)
    $brush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(208, 221, 236, 255))
    try {
        $Graphics.DrawString($Text, $font, $brush, $X, $Y)
    } finally {
        $font.Dispose()
        $brush.Dispose()
    }
}

function Draw-Heading {
    param(
        [System.Drawing.Graphics]$Graphics,
        [string]$Text,
        [float]$X,
        [float]$Y,
        [float]$Size = 40
    )

    $font = New-Object System.Drawing.Font("Microsoft YaHei UI", $Size, [System.Drawing.FontStyle]::Bold)
    $brush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 245, 249, 255))
    try {
        $Graphics.DrawString($Text, $font, $brush, $X, $Y)
    } finally {
        $font.Dispose()
        $brush.Dispose()
    }
}

function Draw-Body {
    param(
        [System.Drawing.Graphics]$Graphics,
        [string]$Text,
        [float]$X,
        [float]$Y,
        [float]$Width,
        [float]$Size = 17
    )

    $font = New-Object System.Drawing.Font("Microsoft YaHei UI", $Size, [System.Drawing.FontStyle]::Regular)
    $brush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(214, 213, 225, 240))
    $format = New-Object System.Drawing.StringFormat
    $rect = [System.Drawing.RectangleF]::new($X, $Y, $Width, 240)
    try {
        $Graphics.DrawString($Text, $font, $brush, $rect, $format)
    } finally {
        $font.Dispose()
        $brush.Dispose()
        $format.Dispose()
    }
}

function Draw-Chip {
    param(
        [System.Drawing.Graphics]$Graphics,
        [string]$Text,
        [float]$X,
        [float]$Y,
        [float]$Width
    )

    $rect = [System.Drawing.RectangleF]::new($X, $Y, $Width, 44)
    $fill = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(38, 129, 85, 255))
    $stroke = New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(82, 170, 122, 255), 1)
    $font = New-Object System.Drawing.Font("Microsoft YaHei UI", 14, [System.Drawing.FontStyle]::Bold)
    $brush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 233, 245, 255))
    $format = New-Object System.Drawing.StringFormat
    try {
        Fill-RoundedRect -Graphics $Graphics -Brush $fill -Rect $rect -Radius 22
        Draw-RoundedRect -Graphics $Graphics -Pen $stroke -Rect $rect -Radius 22
        $format.Alignment = [System.Drawing.StringAlignment]::Center
        $format.LineAlignment = [System.Drawing.StringAlignment]::Center
        $Graphics.DrawString($Text, $font, $brush, $rect, $format)
    } finally {
        $fill.Dispose()
        $stroke.Dispose()
        $font.Dispose()
        $brush.Dispose()
        $format.Dispose()
    }
}

function Draw-StatCard {
    param(
        [System.Drawing.Graphics]$Graphics,
        [string]$Title,
        [string]$Body,
        [float]$X,
        [float]$Y,
        [float]$Width = 170
    )

    $rect = [System.Drawing.RectangleF]::new($X, $Y, $Width, 122)
    $fill = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(48, 20, 16, 40))
    $stroke = New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(70, 132, 111, 194), 1)
    $titleFont = New-Object System.Drawing.Font("Microsoft YaHei UI", 16, [System.Drawing.FontStyle]::Bold)
    $bodyFont = New-Object System.Drawing.Font("Microsoft YaHei UI", 12, [System.Drawing.FontStyle]::Regular)
    $titleBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 240, 246, 255))
    $bodyBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(214, 213, 225, 240))
    try {
        Fill-RoundedRect -Graphics $Graphics -Brush $fill -Rect $rect -Radius 26
        Draw-RoundedRect -Graphics $Graphics -Pen $stroke -Rect $rect -Radius 26
        $Graphics.DrawString($Title, $titleFont, $titleBrush, $X + 20, $Y + 22)
        $bodyRect = [System.Drawing.RectangleF]::new(($X + 20), ($Y + 58), ($Width - 40), 54)
        $Graphics.DrawString($Body, $bodyFont, $bodyBrush, $bodyRect)
    } finally {
        $fill.Dispose()
        $stroke.Dispose()
        $titleFont.Dispose()
        $bodyFont.Dispose()
        $titleBrush.Dispose()
        $bodyBrush.Dispose()
    }
}

function Draw-CodePanel {
    param(
        [System.Drawing.Graphics]$Graphics,
        [float]$X,
        [float]$Y,
        [float]$Width,
        [float]$Height,
        [string]$Title = "Local Automation API",
        [string]$BodyText = @(
            "GET  /api/v1/automation/info",
            "GET  /api/v1/automation/profiles",
            "POST /api/v1/automation/sessions",
            "DELETE /api/v1/automation/sessions/{id}",
            "POST /api/v1/automation/token/rotate"
        ) -join [Environment]::NewLine,
        [string]$BodyFontName = "Consolas",
        [float]$BodyFontSize = 15
    )

    $rect = [System.Drawing.RectangleF]::new($X, $Y, $Width, $Height)
    $fill = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(48, 14, 12, 32))
    $stroke = New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(66, 140, 112, 196), 1)
    $titleFont = New-Object System.Drawing.Font("Microsoft YaHei UI", 15, [System.Drawing.FontStyle]::Bold)
    $monoFont = New-Object System.Drawing.Font($BodyFontName, $BodyFontSize, [System.Drawing.FontStyle]::Regular)
    $titleBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 235, 243, 255))
    $bodyBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(228, 197, 182, 255))
    try {
        Fill-RoundedRect -Graphics $Graphics -Brush $fill -Rect $rect -Radius 28
        Draw-RoundedRect -Graphics $Graphics -Pen $stroke -Rect $rect -Radius 28
        $Graphics.DrawString($Title, $titleFont, $titleBrush, $X + 28, $Y + 24)
        $codeRect = [System.Drawing.RectangleF]::new(($X + 28), ($Y + 66), ($Width - 56), ($Height - 94))
        $Graphics.DrawString($BodyText, $monoFont, $bodyBrush, $codeRect)
    } finally {
        $fill.Dispose()
        $stroke.Dispose()
        $titleFont.Dispose()
        $monoFont.Dispose()
        $titleBrush.Dispose()
        $bodyBrush.Dispose()
    }
}

function Fill-GlassPanel {
    param(
        [System.Drawing.Graphics]$Graphics,
        [float]$X,
        [float]$Y,
        [float]$Width,
        [float]$Height,
        [float]$Radius = 30
    )

    $rect = [System.Drawing.RectangleF]::new($X, $Y, $Width, $Height)
    $fill = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(58, 13, 18, 34))
    $stroke = New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(78, 112, 102, 170), 1)
    try {
        Fill-RoundedRect -Graphics $Graphics -Brush $fill -Rect $rect -Radius $Radius
        Draw-RoundedRect -Graphics $Graphics -Pen $stroke -Rect $rect -Radius $Radius
    } finally {
        $fill.Dispose()
        $stroke.Dispose()
    }
}

function Draw-UiBlock {
    param(
        [System.Drawing.Graphics]$Graphics,
        [float]$X,
        [float]$Y,
        [float]$Width,
        [float]$Height,
        [bool]$Accent = $false,
        [float]$Radius = 18
    )

    $rect = [System.Drawing.RectangleF]::new($X, $Y, $Width, $Height)
    $fillColor = if ($Accent) {
        [System.Drawing.Color]::FromArgb(210, 125, 84, 235)
    } else {
        [System.Drawing.Color]::FromArgb(84, 28, 35, 56)
    }
    $strokeColor = if ($Accent) {
        [System.Drawing.Color]::FromArgb(90, 191, 166, 255)
    } else {
        [System.Drawing.Color]::FromArgb(60, 82, 92, 126)
    }
    $fill = New-Object System.Drawing.SolidBrush $fillColor
    $stroke = New-Object System.Drawing.Pen ($strokeColor, 1)
    try {
        Fill-RoundedRect -Graphics $Graphics -Brush $fill -Rect $rect -Radius $Radius
        Draw-RoundedRect -Graphics $Graphics -Pen $stroke -Rect $rect -Radius $Radius
    } finally {
        $fill.Dispose()
        $stroke.Dispose()
    }
}

function Draw-WindowShell {
    param(
        [System.Drawing.Graphics]$Graphics,
        [float]$X,
        [float]$Y,
        [float]$Width,
        [float]$Height,
        [string]$Title = "MyBrowser"
    )

    $outer = [System.Drawing.RectangleF]::new($X, $Y, $Width, $Height)
    Draw-ShadowPanel -Graphics $Graphics -Rect $outer -Radius 34 -Alpha 28 -Offset 18
    $frameFill = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(238, 8, 12, 24))
    $frameStroke = New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(84, 102, 108, 188), 1.2)
    $topbarFill = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 33, 29, 27))
    $textBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(240, 245, 248, 255))
    $titleFont = New-Object System.Drawing.Font("Microsoft YaHei UI", 12, [System.Drawing.FontStyle]::Bold)
    $dotBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 255, 255, 255))
    try {
        Fill-RoundedRect -Graphics $Graphics -Brush $frameFill -Rect $outer -Radius 34
        Draw-RoundedRect -Graphics $Graphics -Pen $frameStroke -Rect $outer -Radius 34

        $topbarRect = [System.Drawing.RectangleF]::new($X, $Y, $Width, 46)
        Fill-RoundedRect -Graphics $Graphics -Brush $topbarFill -Rect $topbarRect -Radius 34
        $strip = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 12, 19, 38))
        try {
            $Graphics.FillRectangle($strip, $X, $Y + 32, $Width, 14)
        } finally {
            $strip.Dispose()
        }

        $Graphics.FillEllipse($dotBrush, $X + 14, $Y + 13, 18, 18)
        $Graphics.DrawString($Title, $titleFont, $textBrush, $X + 46, $Y + 12)
        $Graphics.FillRectangle($dotBrush, $X + $Width - 120, $Y + 20, 12, 2)
        $Graphics.DrawRectangle((New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(220, 255, 255, 255), 1)), $X + $Width - 82, $Y + 13, 13, 13)
        $Graphics.DrawLine((New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(220, 255, 255, 255), 1)), $X + $Width - 46, $Y + 13, $X + $Width - 32, $Y + 27)
        $Graphics.DrawLine((New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(220, 255, 255, 255), 1)), $X + $Width - 32, $Y + 13, $X + $Width - 46, $Y + 27)
    } finally {
        $frameFill.Dispose()
        $frameStroke.Dispose()
        $topbarFill.Dispose()
        $textBrush.Dispose()
        $titleFont.Dispose()
        $dotBrush.Dispose()
    }
}

function Draw-EnvironmentMock {
    param(
        [System.Drawing.Graphics]$Graphics,
        [float]$X,
        [float]$Y,
        [float]$Width,
        [float]$Height
    )

    Draw-WindowShell -Graphics $Graphics -X $X -Y $Y -Width $Width -Height $Height -Title "my-browser"

    $contentX = $X + 18
    $contentY = $Y + 64
    $sidebarW = 210
    Fill-GlassPanel -Graphics $Graphics -X $contentX -Y $contentY -Width $sidebarW -Height ($Height - 82) -Radius 24
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 26) -Width 96 -Height 18 -Accent $true -Radius 10
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 74) -Width 160 -Height 42 -Accent $true -Radius 20
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 136) -Width 126 -Height 16 -Radius 10
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 178) -Width 110 -Height 16 -Radius 10
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 258) -Width 168 -Height 40 -Radius 18
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 314) -Width 168 -Height 40 -Radius 18
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 370) -Width 168 -Height 40 -Radius 18
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 426) -Width 168 -Height 40 -Radius 18
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 22) -Y ($contentY + 510) -Width 168 -Height 110 -Radius 22

    $mainX = $contentX + $sidebarW + 26
    $mainW = $Width - ($mainX - $X) - 24
    Fill-GlassPanel -Graphics $Graphics -X $mainX -Y $contentY -Width $mainW -Height 140 -Radius 30
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 38) -Y ($contentY + 34) -Width 170 -Height 18 -Radius 10
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 38) -Y ($contentY + 64) -Width 280 -Height 16 -Radius 9
    Draw-UiBlock -Graphics $Graphics -X ($mainX + $mainW - 420) -Y ($contentY + 30) -Width 320 -Height 42 -Radius 20
    Draw-UiBlock -Graphics $Graphics -X ($mainX + $mainW - 212) -Y ($contentY + 94) -Width 152 -Height 46 -Accent $true -Radius 23
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 38) -Y ($contentY + 100) -Width 94 -Height 38 -Radius 19
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 150) -Y ($contentY + 100) -Width 104 -Height 38 -Radius 19
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 272) -Y ($contentY + 100) -Width 78 -Height 38 -Accent $true -Radius 19

    $cardY = $contentY + 176
    $cardW = ($mainW - 18) / 2
    for ($i = 0; $i -lt 4; $i++) {
        $row = [math]::Floor($i / 2)
        $col = $i % 2
        $cx = $mainX + ($cardW + 18) * $col
        $cy = $cardY + 224 * $row
        Fill-GlassPanel -Graphics $Graphics -X $cx -Y $cy -Width $cardW -Height 206 -Radius 28
        Draw-UiBlock -Graphics $Graphics -X ($cx + 28) -Y ($cy + 26) -Width 240 -Height 18 -Radius 10
        Draw-UiBlock -Graphics $Graphics -X ($cx + 28) -Y ($cy + 58) -Width 106 -Height 14 -Radius 9
        Draw-UiBlock -Graphics $Graphics -X ($cx + $cardW - 72) -Y ($cy + 24) -Width 48 -Height 34 -Radius 17
        Draw-UiBlock -Graphics $Graphics -X ($cx + 28) -Y ($cy + 102) -Width 132 -Height 14 -Radius 9
        Draw-UiBlock -Graphics $Graphics -X ($cx + 192) -Y ($cy + 102) -Width 144 -Height 14 -Radius 9
        Draw-UiBlock -Graphics $Graphics -X ($cx + 28) -Y ($cy + 132) -Width ($cardW - 56) -Height 50 -Accent $true -Radius 24
        Draw-UiBlock -Graphics $Graphics -X ($cx + 28) -Y ($cy + 190) -Width 146 -Height 34 -Radius 17
        Draw-UiBlock -Graphics $Graphics -X ($cx + 190) -Y ($cy + 190) -Width 146 -Height 34 -Radius 17
    }
}

function Draw-AutomationMock {
    param(
        [System.Drawing.Graphics]$Graphics,
        [float]$X,
        [float]$Y,
        [float]$Width,
        [float]$Height
    )

    Draw-WindowShell -Graphics $Graphics -X $X -Y $Y -Width $Width -Height $Height -Title "my-browser"

    $contentX = $X + 22
    $contentY = $Y + 72
    $leftW = 250
    Fill-GlassPanel -Graphics $Graphics -X $contentX -Y $contentY -Width $leftW -Height ($Height - 100) -Radius 26
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 24) -Y ($contentY + 24) -Width 150 -Height 18 -Accent $true -Radius 10
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 24) -Y ($contentY + 56) -Width 184 -Height 16 -Radius 9
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 24) -Y ($contentY + 108) -Width ($leftW - 48) -Height 110 -Radius 20
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 24) -Y ($contentY + 236) -Width ($leftW - 48) -Height 44 -Accent $true -Radius 22
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 24) -Y ($contentY + 300) -Width 92 -Height 14 -Radius 8
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 24) -Y ($contentY + 324) -Width 152 -Height 12 -Radius 8
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 24) -Y ($contentY + 368) -Width ($leftW - 48) -Height 92 -Radius 20
    Draw-UiBlock -Graphics $Graphics -X ($contentX + 24) -Y ($contentY + 482) -Width ($leftW - 48) -Height 40 -Radius 20

    $mainX = $contentX + $leftW + 22
    $mainW = $Width - ($mainX - $X) - 26
    Fill-GlassPanel -Graphics $Graphics -X $mainX -Y $contentY -Width $mainW -Height 118 -Radius 26
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 26) -Y ($contentY + 26) -Width 188 -Height 18 -Radius 9
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 26) -Y ($contentY + 54) -Width 292 -Height 16 -Radius 9
    Draw-UiBlock -Graphics $Graphics -X ($mainX + $mainW - 184) -Y ($contentY + 34) -Width 136 -Height 44 -Accent $true -Radius 22

    $panelY = $contentY + 146
    Fill-GlassPanel -Graphics $Graphics -X $mainX -Y $panelY -Width $mainW -Height 180 -Radius 28
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 26) -Y ($panelY + 24) -Width 134 -Height 16 -Radius 9
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 26) -Y ($panelY + 52) -Width ($mainW - 52) -Height 48 -Radius 22
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 26) -Y ($panelY + 118) -Width 160 -Height 38 -Accent $true -Radius 19
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 206) -Y ($panelY + 118) -Width 160 -Height 38 -Radius 19

    Fill-GlassPanel -Graphics $Graphics -X $mainX -Y ($panelY + 206) -Width $mainW -Height 228 -Radius 28
    Draw-UiBlock -Graphics $Graphics -X ($mainX + 26) -Y ($panelY + 230) -Width 168 -Height 16 -Radius 9
    for ($i = 0; $i -lt 6; $i++) {
        $lineY = $panelY + 264 + ($i * 24)
        $lineW = if ($i % 2 -eq 0) { $mainW - 92 } else { $mainW - 160 }
        Draw-UiBlock -Graphics $Graphics -X ($mainX + 26) -Y $lineY -Width $lineW -Height 12 -Radius 7
    }
}

function Draw-ImageCard {
    param(
        [System.Drawing.Graphics]$Graphics,
        [System.Drawing.Image]$Image,
        [System.Drawing.RectangleF]$DestRect,
        [System.Drawing.RectangleF]$SourceRect,
        [float]$Radius = 30
    )

    Draw-ShadowPanel -Graphics $Graphics -Rect $DestRect -Radius $Radius -Alpha 24 -Offset 18
    $path = New-RoundedPath -Rect $DestRect -Radius $Radius
    $stroke = New-Object System.Drawing.Pen ([System.Drawing.Color]::FromArgb(70, 174, 126, 226), 1.4)
    try {
        $Graphics.SetClip($path)
        $Graphics.DrawImage($Image, $DestRect, $SourceRect, [System.Drawing.GraphicsUnit]::Pixel)
        $overlayBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(16, 7, 16, 31))
        try {
            $Graphics.FillRectangle($overlayBrush, $DestRect)
        } finally {
            $overlayBrush.Dispose()
        }
        $Graphics.ResetClip()
        Draw-RoundedRect -Graphics $Graphics -Pen $stroke -Rect $DestRect -Radius $Radius
    } finally {
        $stroke.Dispose()
        $path.Dispose()
        $Graphics.ResetClip()
    }
}

function New-Canvas {
    param(
        [int]$Width = 1600,
        [int]$Height = 900
    )

    $bitmap = New-Object System.Drawing.Bitmap($Width, $Height)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $graphics.TextRenderingHint = [System.Drawing.Text.TextRenderingHint]::ClearTypeGridFit
    return @{ Bitmap = $bitmap; Graphics = $graphics }
}

function Save-Canvas {
    param(
        [hashtable]$Canvas,
        [string]$Path
    )

    try {
        $Canvas.Bitmap.Save($Path, [System.Drawing.Imaging.ImageFormat]::Png)
    } finally {
        $Canvas.Graphics.Dispose()
        $Canvas.Bitmap.Dispose()
    }
}

$repoRoot = (Resolve-Path ".").Path
$sourcePath = Join-Path $repoRoot $SourceScreenshot
$backdropPath = Join-Path $repoRoot $Backdrop
$outputPath = Join-Path $repoRoot $OutputDir

if (-not (Test-Path $sourcePath)) {
    throw "Source screenshot not found: $sourcePath"
}
if (-not (Test-Path $backdropPath)) {
    throw "Backdrop not found: $backdropPath"
}

New-Item -ItemType Directory -Force -Path $outputPath | Out-Null

$backdropImage = [System.Drawing.Image]::FromFile($backdropPath)

try {
    $cover = New-Canvas;
    $cover.Graphics.DrawImage($backdropImage, 0, 0, 1600, 900);
    Draw-Chip -Graphics $cover.Graphics -Text "v1.0.1 Release" -X 92 -Y 88 -Width 168;
    Draw-Heading -Graphics $cover.Graphics -Text "MyBrowser" -X 92 -Y 172 -Size 56;
    Draw-Heading -Graphics $cover.Graphics -Text "本地化环境管理" -X 92 -Y 246 -Size 30;
    Draw-Heading -Graphics $cover.Graphics -Text "与自动化工作台" -X 92 -Y 292 -Size 30;
    Draw-Body -Graphics $cover.Graphics -Text "围绕环境隔离、Cookie 持久化与 Local Automation API 打造，适合需要本地掌控与可重复流程的多环境使用场景。" -X 92 -Y 360 -Width 490 -Size 17;
    Draw-Chip -Graphics $cover.Graphics -Text "环境分类" -X 92 -Y 474 -Width 128;
    Draw-Chip -Graphics $cover.Graphics -Text "Cookie 同步" -X 238 -Y 474 -Width 148;
    Draw-Chip -Graphics $cover.Graphics -Text "BiDi 一键打开" -X 404 -Y 474 -Width 176;
    Draw-EnvironmentMock -Graphics $cover.Graphics -X 628 -Y 122 -Width 850 -Height 638;
    Draw-StatCard -Graphics $cover.Graphics -Title "Local API" -Body "127.0.0.1 本地监听`nBearer Token 鉴权" -X 628 -Y 792 -Width 250;
    Draw-StatCard -Graphics $cover.Graphics -Title "便携模式" -Body "portable.flag`nMyBrowserData" -X 900 -Y 792 -Width 250;
    Draw-StatCard -Graphics $cover.Graphics -Title "工作流" -Body "启动环境`n同步状态`n自动化接入" -X 1172 -Y 792 -Width 250;
    Save-Canvas -Canvas $cover -Path (Join-Path $outputPath "release-cover.png");

    $profiles = New-Canvas;
    $profiles.Graphics.DrawImage($backdropImage, 0, 0, 1600, 900);
    Draw-Chip -Graphics $profiles.Graphics -Text "环境工作台" -X 92 -Y 86 -Width 156;
    Draw-Heading -Graphics $profiles.Graphics -Text "环境管理更清晰" -X 92 -Y 168 -Size 48;
    Draw-Body -Graphics $profiles.Graphics -Text "分类、搜索与详情管理被收进一套更直观的工作台视图，日常切换与批量维护都更顺手。" -X 92 -Y 246 -Width 520 -Size 17;
    Draw-StatCard -Graphics $profiles.Graphics -Title "分类筛选" -Body "按分类整理环境，未分类也能单独查看。" -X 92 -Y 392 -Width 186;
    Draw-StatCard -Graphics $profiles.Graphics -Title "快速管理" -Body "常用动作留在首页，细操作进入详情页。" -X 296 -Y 392 -Width 186;
    Draw-StatCard -Graphics $profiles.Graphics -Title "本地持久化" -Body "默认安装与便携模式都支持独立数据目录。" -X 500 -Y 392 -Width 186;
    Draw-EnvironmentMock -Graphics $profiles.Graphics -X 760 -Y 86 -Width 748 -Height 728;
    Draw-CodePanel -Graphics $profiles.Graphics -X 92 -Y 590 -Width 594 -Height 182 -Title "工作台要点" -BodyText ("按分类筛选环境`n在详情页集中做设置与 Cookie 管理`n默认安装与便携模式都支持独立数据目录") -BodyFontName "Microsoft YaHei UI" -BodyFontSize 14;
    Save-Canvas -Canvas $profiles -Path (Join-Path $outputPath "release-profiles.png");

    $automation = New-Canvas;
    $automation.Graphics.DrawImage($backdropImage, 0, 0, 1600, 900);
    Draw-Chip -Graphics $automation.Graphics -Text "Automation" -X 92 -Y 86 -Width 152;
    Draw-Heading -Graphics $automation.Graphics -Text "把浏览器接进" -X 92 -Y 168 -Size 40;
    Draw-Heading -Graphics $automation.Graphics -Text "本地自动化流程" -X 92 -Y 220 -Size 40;
    Draw-Body -Graphics $automation.Graphics -Text "MyBrowser 提供 Local Automation API、Bearer Token 鉴权与基于 Firefox / Camoufox 的 BiDi 接入，内嵌贝塞尔行为仿真算法与自动防检测校验套件，助自动化流程平滑通过风控。" -X 92 -Y 296 -Width 560 -Size 17;
    Draw-StatCard -Graphics $automation.Graphics -Title "只监听本地" -Body "默认绑定 127.0.0.1，减少调试口暴露面。" -X 92 -Y 430 -Width 194;
    Draw-StatCard -Graphics $automation.Graphics -Title "Token 鉴权" -Body "控制台可查看与轮换 Bearer Token。" -X 304 -Y 430 -Width 194;
    Draw-StatCard -Graphics $automation.Graphics -Title "拟真防检测" -Body "内置贝塞尔鼠标移动、高斯点击与离线谎言审计。" -X 516 -Y 430 -Width 194;

    Draw-CodePanel -Graphics $automation.Graphics -X 92 -Y 592 -Width 618 -Height 208;
    Draw-AutomationMock -Graphics $automation.Graphics -X 826 -Y 116 -Width 688 -Height 632;
    Draw-Chip -Graphics $automation.Graphics -Text "Local Automation API" -X 1068 -Y 780 -Width 250;
    Draw-Chip -Graphics $automation.Graphics -Text "BiDi 一键打开" -X 1334 -Y 780 -Width 150;
    Save-Canvas -Canvas $automation -Path (Join-Path $outputPath "release-automation.png");
} finally {
    $backdropImage.Dispose()
}
