<#
  一键构建：前端 → 后端（桌面 + 浏览器）→ 安装包
  用法：
    ./build.ps1                 全部构建
    ./build.ps1 -SkipInstaller  只构建前端与可执行文件
#>
param(
  [switch]$SkipInstaller,
  [string]$Version = "v1.2"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

function Step($name) { Write-Host "== $name ==" -ForegroundColor Cyan }

Step "前端构建"
Push-Location web
npm ci
npm run build
Pop-Location

Step "后端构建（浏览器模式）"
$env:CGO_ENABLED = "0"
go build -trimpath -o ledger.exe ./cmd/server

Step "后端构建（桌面模式）"
go build -trimpath -ldflags "-H windowsgui -s -w" -o ledger-desktop.exe ./cmd/desktop

if (-not $SkipInstaller) {
  Step "安装包"
  Push-Location installer-kit
  go run ./build -profile $Version -src ..
  Pop-Location
}

Write-Host "`n完成。" -ForegroundColor Green
Get-ChildItem -Path . -Filter *.exe | Select-Object Name, @{n='MB';e={[math]::Round($_.Length/1MB,1)}}
