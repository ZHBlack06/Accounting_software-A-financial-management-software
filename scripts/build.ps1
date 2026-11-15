# =============================================================================
# build.ps1 —— 一条命令完成全部构建：
#   前端  web/  → npm ci（或 npm install）→ npm run build → 产出 web/dist
#   后端  Go    → CGO_ENABLED=0 go build（离线优先，失败回退 goproxy.cn）→ ledger.exe
# 用法：
#   powershell -ExecutionPolicy Bypass -File scripts/build.ps1
# 可选参数：
#   -Linux    产出 Linux 二进制 `ledger`（供 Docker/服务器），默认 Windows `ledger.exe`
# =============================================================================
param(
    [switch]$Linux
)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

function Step($message) { Write-Host "`n==> $message" -ForegroundColor Cyan }

# ---------------------------------------------------------------------------
Step "前端构建（web/dist）"
if (Test-Path "$Root\web\package.json") {
    Push-Location "$Root\web"
    try {
        if (Test-Path "package-lock.json") {
            npm ci
            if ($LASTEXITCODE -ne 0) { throw "npm ci 失败（exit $LASTEXITCODE）" }
        } else {
            Write-Warning "web/package-lock.json 不存在，使用 npm install（首次锁定版本后建议改回 npm ci）"
            npm install
            if ($LASTEXITCODE -ne 0) { throw "npm install 失败（exit $LASTEXITCODE）" }
        }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw "npm run build 失败（exit $LASTEXITCODE）" }
    }
    finally { Pop-Location }
    if (-not (Test-Path "$Root\web\dist\index.html")) {
        throw "前端构建完成但 web/dist/index.html 不存在，请检查 vite 配置"
    }
    Write-Host "前端产物：web/dist" -ForegroundColor Green
} else {
    Write-Warning "web/package.json 不存在（前端尚未就绪），跳过前端构建"
}

# ---------------------------------------------------------------------------
Step "后端构建（CGO 必须为 0 —— 纯 Go SQLite 驱动，禁止 cgo）"
$env:CGO_ENABLED = "0"
$env:GOFLAGS = "-mod=mod"
$env:GOSUMDB = "off"
if ($Linux) {
    $env:GOOS = "linux"
    $Output = "ledger"
} else {
    $env:GOOS = "windows"
    $Output = "ledger.exe"
}

# Go 依赖只能离线解析：先 GOPROXY=off 走本地 module cache，
# 失败时回退 goproxy.cn（再失败则报错退出，不静默）。
$env:GOPROXY = "off"
$BuildArgs = @("build", "-trimpath", "-ldflags=-s -w", "-o", $Output, "./cmd/server")

Step "go build（离线：GOPROXY=off）"
go @BuildArgs
if ($LASTEXITCODE -ne 0) {
    Write-Warning "离线构建失败，回退 GOPROXY=https://goproxy.cn,direct 重试 ..."
    $env:GOPROXY = "https://goproxy.cn,direct"
    go @BuildArgs
    if ($LASTEXITCODE -ne 0) { throw "go build 失败（goproxy.cn 亦不可用，请检查网络或本地 module cache）" }
}
$Binary = Get-Item "$Root\$Output" -ErrorAction SilentlyContinue
if ($null -eq $Binary) { throw "未找到构建产物 $Output" }
$SizeMB = [math]::Round($Binary.Length / 1MB, 1)
Write-Host "后端产物：$Output（$SizeMB MB）" -ForegroundColor Green

# ---------------------------------------------------------------------------
# v1.1 桌面版（-H windowsgui：双击无控制台，内置 WebView2 窗口）
if (-not $Linux) {
    Step "桌面版构建（ledger-desktop.exe，windowsgui 单文件）"
    $env:GOOS = "windows"
    $DesktopArgs = @("build", "-trimpath", "-ldflags=-H windowsgui -s -w", "-o", "ledger-desktop.exe", "./cmd/desktop")
    go @DesktopArgs
    if ($LASTEXITCODE -ne 0) { throw "桌面版 go build 失败（exit $LASTEXITCODE）" }
    $DesktopBin = Get-Item "$Root\ledger-desktop.exe" -ErrorAction SilentlyContinue
    if ($null -eq $DesktopBin) { throw "未找到构建产物 ledger-desktop.exe" }
    $DesktopMB = [math]::Round($DesktopBin.Length / 1MB, 1)
    Write-Host "桌面版产物：ledger-desktop.exe（$DesktopMB MB）" -ForegroundColor Green
}

# 恢复默认 GOOS（避免污染当前会话影响后续命令）
$env:GOOS = ""

Write-Host "`n构建完成：ledger-desktop.exe / ledger.exe / ledger + web/dist" -ForegroundColor Green
Write-Host "桌面版：双击 ledger-desktop.exe（详见 README-DESKTOP.md）"
Write-Host "浏览器版：powershell -File scripts\start.ps1   或   docker compose up -d"
