# =============================================================================
# dev-frontend.ps1 —— 开发模式启动前端（vite 热更新，127.0.0.1:5173）
# /api 代理到本地后端 127.0.0.1:8770（见 web/vite.config.ts）。
# 请先用 dev-backend.ps1 启动后端，再开本窗口。
# 用法：powershell -ExecutionPolicy Bypass -File scripts/dev-frontend.ps1
# =============================================================================
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location "$Root\web"

if (-not (Test-Path "node_modules")) {
    Write-Host "==> 首次运行：安装前端依赖" -ForegroundColor Cyan
    if (Test-Path "package-lock.json") { npm ci } else { npm install }
    if ($LASTEXITCODE -ne 0) { throw "npm 依赖安装失败" }
}

Write-Host "==> npm run dev（http://127.0.0.1:5173，/api → 127.0.0.1:8770）" -ForegroundColor Cyan
npm run dev
