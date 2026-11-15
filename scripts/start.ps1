# =============================================================================
# start.ps1 —— 启动本地服务并自动打开浏览器
#   1. ledger.exe / web/dist 缺失时自动先执行 build.ps1
#   2. 启动 OCR 服务（127.0.0.1:8771，可选 -NoOcr 跳过；Python 不可用时忽略）
#   3. 启动后端（127.0.0.1:8770）
#   4. 等待 /api/health 就绪 → 打开 http://127.0.0.1:8770
# 停止：直接关闭两个控制台窗口，或 taskkill /IM ledger.exe /F
# =============================================================================
param(
    [switch]$NoOcr,
    [switch]$NoBuild
)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

function Step($message) { Write-Host "`n==> $message" -ForegroundColor Cyan }

# ---------------------------------------------------------------------------
# 0. 按需构建
if (-not $NoBuild) {
    $NeedBuild = (-not (Test-Path "$Root\ledger.exe"))
    if (-not (Test-Path "$Root\web\dist\index.html")) { $NeedBuild = $true }
    if ($NeedBuild) {
        Step "产物缺失，先执行 scripts/build.ps1"
        & powershell -NoProfile -ExecutionPolicy Bypass -File "$PSScriptRoot\build.ps1"
        if ($LASTEXITCODE -ne 0) { throw "构建失败，中止启动" }
    }
}

# ---------------------------------------------------------------------------
# 1. OCR 服务（可选；失败不影响后端启动 —— 后端对 OCR 不可用返回 503 降级）
$OcrProcess = $null
if (-not $NoOcr) {
    if (Test-Path "$Root\ocr-service\app\main.py") {
        Step "启动 OCR 服务（127.0.0.1:8771，mock 引擎兜底）"
        try {
            $OcrProcess = Start-Process -FilePath "python" `
                -ArgumentList "-m", "uvicorn", "app.main:app", "--host", "127.0.0.1", "--port", "8771", "--workers", "1" `
                -WorkingDirectory "$Root\ocr-service" `
                -WindowStyle Minimized -PassThru
            Write-Host "OCR 进程 PID：$($OcrProcess.Id)（最小化窗口，关闭即停止）"
        } catch {
            Write-Warning "OCR 服务启动失败（Python/uvicorn 不可用？），后端 OCR 功能将返回 503 降级：$_"
            $OcrProcess = $null
        }
    } else {
        Write-Warning "ocr-service/ 不存在，跳过 OCR 启动"
    }
}

# ---------------------------------------------------------------------------
# 2. 后端
Step "启动后端（127.0.0.1:8770，数据目录 data/）"
$Backend = Start-Process -FilePath "$Root\ledger.exe" `
    -WorkingDirectory $Root -WindowStyle Minimized -PassThru
Write-Host "后端进程 PID：$($Backend.Id)"

# ---------------------------------------------------------------------------
# 3. 等待就绪并打开浏览器（最多 30s）
Step "等待 /api/health 就绪"
$Ready = $false
for ($i = 0; $i -lt 60; $i++) {
    try {
        $resp = Invoke-WebRequest -Uri "http://127.0.0.1:8770/api/health" `
            -UseBasicParsing -TimeoutSec 2 -ErrorAction Stop
        if ($resp.StatusCode -eq 200) { $Ready = $true; break }
    } catch { Start-Sleep -Milliseconds 500 }
}
if (-not $Ready) {
    Write-Warning "后端 30 秒内未就绪，请查看后端窗口日志（仍尝试打开浏览器）"
} else {
    Write-Host "后端已就绪：$($resp.Content)" -ForegroundColor Green
}

# ---------------------------------------------------------------------------
# 3.5 读取并展示访问令牌（首次启动自动生成；登录页需要粘贴此令牌）
Step "读取访问令牌"
try {
    # 令牌走 stdout，日志走 stderr；Select-Object -Last 1 兜底避免任何杂输出污染
    $tok = (& "$Root\ledger.exe" --print-token 2>$null | Select-Object -Last 1)
} catch {
    $tok = ""
}
if ([string]::IsNullOrWhiteSpace($tok)) {
    Write-Warning "未能读取访问令牌，请手动执行：.\ledger.exe --print-token"
} else {
    Write-Host ""
    Write-Host "访问令牌（登录用）: $tok" -ForegroundColor Cyan
    Write-Host "浏览器打开 http://127.0.0.1:8770，粘贴此令牌登录" -ForegroundColor Gray
    Write-Host ""
}

Step "打开浏览器"
Start-Process "http://127.0.0.1:8770"

Write-Host "`n启动完成。停止方式：" -ForegroundColor Green
Write-Host "  taskkill /IM ledger.exe /F"
if ($null -ne $OcrProcess) { Write-Host "  taskkill /PID $($OcrProcess.Id) /F   （OCR）" }
