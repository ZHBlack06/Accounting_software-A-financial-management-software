# =============================================================================
# dev-backend.ps1 —— 开发模式启动后端（go run，改代码重跑即可）
# 监听 127.0.0.1:8770，数据目录 data/（开发库）；OCR 端点默认 127.0.0.1:8771。
# 用法：powershell -ExecutionPolicy Bypass -File scripts/dev-backend.ps1
# =============================================================================
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

# CGO 必须为 0：glebarez/sqlite 为纯 Go 驱动，开启 cgo 在无 GCC 环境会构建失败
$env:CGO_ENABLED = "0"
$env:GOFLAGS = "-mod=mod"
$env:GOSUMDB = "off"
# 离线优先；如需拉取新依赖，环境变量 GOPROXY=https://goproxy.cn,direct 后重跑
if (-not $env:GOPROXY) { $env:GOPROXY = "off" }

Write-Host "==> go run ./cmd/server（127.0.0.1:8770，GOPROXY=$env:GOPROXY）" -ForegroundColor Cyan
go run ./cmd/server
if ($LASTEXITCODE -ne 0) {
    Write-Warning "离线解析失败，回退 goproxy.cn 重试 ..."
    $env:GOPROXY = "https://goproxy.cn,direct"
    go run ./cmd/server
}
