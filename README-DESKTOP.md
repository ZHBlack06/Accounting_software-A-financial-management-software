> **v1.2 桌面版变更提示（读本文 v1.1 部分时注意）**
> - 窗口标题改为**运行时取 `config.Version`**：v1.2 显示「个人记账 v1.2」，不再硬编码（下方 v1.1 章节里的「个人记账 v1.1」为历史描述）。
> - 单实例互斥量改为 `Local\JizhangDesktopV12`（v1.1 是 `...V11`）：**v1.1 与 v1.2 可同时运行**，不会再出现「双击 v1.2 却弹出 v1.1 窗口」。数据目录仍共用 `%LOCALAPPDATA%\个人记账\data`，升级即延续。
> - `meta.app_version` 现在升级时**会被显式覆盖**为当前版本（原先 `INSERT OR IGNORE` 导致老库永远显示旧版本）。
>
# 个人记账 v1.1 桌面版说明（README-DESKTOP）

v1.1 在 v1.0（浏览器 + 后端服务）基础上增加了一个**独立窗口桌面版**：
双击 `ledger-desktop.exe` 即可打开应用窗口，不需要浏览器、不需要记端口、不需要手动粘贴访问令牌。

---

## 1. 运行要求

| 项 | 要求 |
| --- | --- |
| 操作系统 | Windows 10 1803+ / Windows 11（本项目仅构建 Windows 版） |
| 浏览器内核 | Microsoft Edge **WebView2 运行时**（Win11 与常规更新的 Win10 自带；缺失时程序会弹窗并给出下载链接） |
| 其他 | 无。单个 exe 内含后端、前端资源与 WebView2Loader.dll，无需安装 Go/Node/Python |

若提示缺少 WebView2，安装地址：<https://go.microsoft.com/fwlink/p/?LinkId=2124703>（安装后重新双击 exe 即可）。

---

## 2. 使用方式

1. 双击 `ledger-desktop.exe`；
2. 约 1 秒内出现标题为「个人记账 v1.1」的窗口，**直接进入首页**（首次启动已自动写入访问令牌）；
3. 关闭窗口即退出程序，内置服务随之关闭，端口释放。

重复双击不会开出第二个窗口：程序用命名互斥量 `Local\JizhangDesktopV11` 检测单实例，
若已有实例在运行，会把已有窗口还原并前置，然后自身以退出码 0 结束。

### 数据目录

优先级：`--data-dir` > 环境变量 `LEDGER_DATA_DIR` > 系统默认

系统默认：`%LOCALAPPDATA%\个人记账\data`（例如 `C:\Users\<用户名>\AppData\Local\个人记账\data`）

目录内容：

```
data\
├─ ledger.db                 # SQLite 主库（纯 Go 驱动，WAL 模式）
├─ ledger.db-wal / -shm
├─ backups\                  # 周备份、手动备份、pre-restore 安全网
├─ screenshots\originals\    # 原始截图（按保留策略清理）
├─ screenshots\compressed\   # 压缩后截图
├─ ocr-results\              # OCR 结果 JSON
├─ tmp\import\               # 导入临时文件
├─ web-assets\<内容哈希>\     # 从 exe 内嵌资源解压的前端（同版本复用，不重复覆盖）
├─ webview2\                 # WebView2 浏览器缓存/配置
├─ window.json               # 上次窗口尺寸（可选，用于下次恢复）
└─ logs\desktop-YYYYMMDD.log # 桌面版日志（JSON 行，保留最近 7 天）
```

> 桌面版是 `-H windowsgui` 构建，**没有控制台窗口**，排查问题请看
> `%LOCALAPPDATA%\个人记账\data\logs\desktop-YYYYMMDD.log`
> （日志含每个 HTTP 请求的 `path`/`status`，可据此确认前端调用了哪些接口）。

---

## 3. 从 v1.0 迁移数据

v1.1 的数据库结构与 v1.0 完全兼容（`schema_version=1`），迁移就是**搬目录**：

1. 关闭 v1.0 的后端服务（确保没有进程占用 `ledger.db`）；
2. 把 v1.0 的 `data\` 目录**整体**复制到 `%LOCALAPPDATA%\个人记账\`，并改名为 `data`：

   ```powershell
   # 示例：v1.0 在 D:\Project\记账\记账v1.0\data
   $dst = Join-Path $env:LOCALAPPDATA '个人记账'
   New-Item -ItemType Directory -Force -Path $dst | Out-Null
   Copy-Item -Recurse -Force 'D:\Project\记账\记账v1.0\data' (Join-Path $dst 'data')
   ```

3. 双击 `ledger-desktop.exe`。桌面版启动时会自动跑 migrate / seed / 视图自检，
   已有 `auth_token` 会被沿用（用户无感知，界面自动登录）。

**不要**只复制 `ledger.db`：`screenshots/`、`backups/` 等子目录也属于数据的一部分。
迁移前建议在 v1.0 里先做一次「创建备份」，迁移后可在设置页用该备份回滚。

---

## 4. 命令行参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--data-dir <path>` | `%LOCALAPPDATA%\个人记账\data` | 数据目录 |
| `--port <n>` | `8770` | 首选监听端口；被占用时自动改用系统分配的空闲端口 |
| `--debug` | `false` | 开启 WebView2 右键菜单与 DevTools（排查前端问题用） |

环境变量：`LEDGER_DATA_DIR`（数据目录）、`LEDGER_OCR_ENDPOINT` / `OCR_BASE_URL`（OCR 服务地址）、
`LEDGER_WEB_DIR`（静态资源目录，桌面版会被内嵌资源目录覆盖）。

端口说明：桌面版固定监听 `127.0.0.1`。默认优先用 `8770`（与 v1.0 习惯一致），
若 `8770` 已被占用（例如 v1.0 后端还在跑），会自动改成内核分配的空闲端口，
真实端口记录在启动日志的 `内置服务已就绪 addr=...` 一行。

---

## 5. 构建

```bash
# 桌面版（无控制台、单文件、纯 Go 无 cgo）
CGO_ENABLED=0 GOOS=windows go build -trimpath \
  -ldflags "-H windowsgui -s -w" -o ledger-desktop.exe ./cmd/desktop

# 浏览器模式（v1.1 仍保留，便于排查问题与开发者使用）
CGO_ENABLED=0 GOOS=windows go build -o ledger.exe ./cmd/server
# 启动（WebDir 默认 web/dist，可用 --web-dir 指定）
./ledger.exe --port 8770            # 浏览器打开 http://127.0.0.1:8770
./ledger.exe --print-token          # 只输出访问令牌（供脚本/首次登录）
```

离线/受限网络下如需重拉依赖：`GOPROXY=https://goproxy.cn,direct GOFLAGS=-mod=mod go mod download`。

桌面版双击等价于 `ledger-desktop.exe`（不带参数）。等价的开发态验证方式：

```powershell
Start-Process .\ledger-desktop.exe
Start-Sleep -Seconds 5
Get-Process ledger-desktop | Select-Object Id, MainWindowTitle, Responding
# 期望：MainWindowTitle = 个人记账 v1.1，Responding = True
```

---

## 6. 与 v1.0 的差异

| 项 | v1.0 | v1.1 |
| --- | --- | --- |
| 打开方式 | 浏览器访问 `127.0.0.1:8770` | 双击 exe，独立窗口（WebView2） |
| 登录 | 手动粘贴访问令牌 | 自动注入令牌到 `localStorage`，打开即首页 |
| 端口 | 固定 8770（占用则启动失败） | 优先 8770，占用则自动分配并取回真实端口 |
| 数据目录 | 工作目录下 `data\` | `%LOCALAPPDATA%\个人记账\data`（可用 `--data-dir` 覆盖） |
| 前端资源 | 依赖 `web/dist` 目录存在 | 内嵌进 exe，按内容哈希解压到数据目录，离线可用 |
| 多开 | 可启动多个进程争抢同一端口 | 单实例：第二次启动唤出已有窗口并退出 |
| 静态资源 404（v1.0 P2-4） | `/assets/*.js` 不存在时返回 `200 + index.html`，导致白屏且难以定位 | **已修复**：带扩展名的未命中路径返回 404；无扩展名的前端路由才回落 `index.html`；`/api/` 前缀仍是 JSON 404 |
| 版本号 | `1.0.0` | `1.1.0`（`/api/health` 的 `version` 字段） |
| 装配代码 | `cmd/server/main.go` 内联装配 | 抽出 `internal/app`，`cmd/server` 与 `cmd/desktop` 共用同一套迁移/启动任务/路由 |

浏览器模式在 v1.1 中**仍然可用**（`ledger.exe`），行为与 v1.0 一致（7 步启动日志、
`--print-token`、优雅退出、`LEDGER_*` 环境变量）。

---

## 7. 常见问题

**Q：双击没反应 / 窗口一闪而过？**
看 `%LOCALAPPDATA%\个人记账\data\logs\desktop-YYYYMMDD.log` 里 `桌面版退出` 一行的 `code`。

**Q：弹窗提示缺少 WebView2？**
按第 1 节的链接安装运行时后重试（Win10 1803 以下系统不支持）。

**Q：能否把数据放在 U 盘 / 指定目录？**
可以：`ledger-desktop.exe --data-dir "E:\记账数据"`，或设置环境变量 `LEDGER_DATA_DIR`。

**Q：想同时开着 v1.0 和 v1.1？**
可以，v1.1 会自动避开被占用的 8770；但两者**不要指向同一个 `data` 目录**（SQLite 单写者，
并发写同一库存在风险）。

**Q：窗口大小记不住？**
尺寸在正常退出（关闭窗口）时写入 `window.json`；强制杀进程不会保存。

## 使用手册

随安装包分发一份离线 HTML 手册 `使用手册.html`（安装目录根下），也可从开始菜单「个人记账 v1.2 → 使用手册」直接打开；内容涵盖安装、记账、Excel/CSV 账本导入、延迟冻结、设置项说明与常见问题。
