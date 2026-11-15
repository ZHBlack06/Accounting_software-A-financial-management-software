# 个人记账 v1.2（桌面版）

> 一个离线优先、单机可用的个人记账软件：桌面独立窗口、无需浏览器与账号；支持把 Excel / CSV 旧账本一次性导入，导入的历史数据「先核对、确认后才冻结」。数据全部保存在本机 SQLite。

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3.5-42B883?logo=vue.js&logoColor=white)](https://vuejs.org/)
[![SQLite](https://img.shields.io/badge/SQLite-embedded-003B57?logo=sqlite&logoColor=white)](https://sqlite.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](#license)

---

## 功能

| 模块 | 说明 |
| --- | --- |
| **桌面版** | Go + WebView2 独立窗口，双击即用；无浏览器、无地址栏；单实例；窗口尺寸记忆 |
| **记账** | 支出 / 收入 / 内部转账；两级分类、标签、商户、账本；物品明细（原价/实付/折扣）；快捷模板 |
| **账本导入** | `.xlsx` / `.xlsm` / `.csv` / 表格型 `.txt`；**多文件、多工作表任选**；**逐列自选映射**（日期/金额/分类/账户/标签/备注/方向）；缺失的分类与标签自动创建 |
| **时间处理** | 源数据只有日期时，同日多条**按原始行序在一天内平均分布**；行级标记为「系统分布」，不会被当成用户录入的精确时间 |
| **时间显示** | 设置里切换 `年月日时分秒` / `年月日`，即时生效，只影响展示 |
| **延迟冻结** | 超过冻结天数的记录默认只读；**导入的历史数据先保持可编辑**，用户确认后才锁定（物化边界钳制，余额与统计不受确认动作影响） |
| **统计** | 仪表盘（收支、余额走势、分类排行、高额消费）、按月/年报表 |
| **数据安全** | 本地 SQLite；备份/恢复；结构升级与账户合并前自动备份；冻结写保护；回收站 |
| **安装包** | 自带安装器（Go 编写）：自定义安装位置、快捷方式、「应用」列表卸载项；卸载默认保留账本数据 |

完整操作说明见 **[使用手册.html](使用手册.html)**（离线 HTML，随安装包分发）。

---

## 技术栈

- **后端**：Go 1.22+、Gin、GORM、纯 Go SQLite（`glebarez/sqlite`，无需 cgo）
- **前端**：Vue 3 + TypeScript + Vite、Element Plus、ECharts、Pinia
- **桌面壳**：`jchv/go-webview2`（纯 Go，WebView2 Loader 已内嵌，无 cgo）
- **Excel 解析**：`xuri/excelize/v2`
- **安装器**：自研 Go 安装器（`installer-kit/`），单文件 exe，内嵌 payload

```
浏览器模式：ledger.exe  →  内置 HTTP 服务 + 前端静态资源
桌面模式：  ledger-desktop.exe → 装配同一套服务 + WebView2 窗口 + 内嵌前端
```

---

## 目录结构

```
.
├─ cmd/
│  ├─ server/          浏览器模式入口（ledger.exe）
│  └─ desktop/         桌面模式入口（ledger-desktop.exe，含图标资源 rsrc.syso）
├─ internal/
│  ├─ app/             装配层：server 与 desktop 共用
│  ├─ config/          版本号、路径、环境变量
│  ├─ desktop/         WebView2 窗口、单实例、资源解压、日志
│  ├─ http/            Gin 路由、handler、DTO、静态托管（SPA fallback）
│  ├─ service/         记账、余额、物化、导入、备份、账户合并、冻结
│  ├─ store/           schema / 迁移 / 仓储（SQLite）
│  └─ pkg/             sheetparse（Excel/CSV 解析）、csvparse、ids、money、timeutil
├─ web/                Vue 3 前端（src/，构建产物 dist/ 不入库）
├─ installer-kit/      Go 安装器源码（构建安装包）
├─ ocr-service/        可选 OCR 服务（Python）
├─ scripts/            冒烟测试等
├─ docs/               架构与实现笔记
└─ assets/             应用图标（svg 生成脚本 / 多尺寸 ico / rc）
```

---

## 本地构建

### 依赖
- Go **1.22+**
- Node.js **20+**（仅构建前端需要）
- Windows（桌面壳与安装器依赖 Win32 API）

### 一键构建（Windows PowerShell）

```powershell
./build.ps1            # 前端 → 后端 → 安装包
./build.ps1 -SkipInstaller
```

### 手动构建

```bash
# 1) 前端（必须先构建：go:embed 需要 web/dist 存在）
cd web
npm ci
npm run build          # 输出 web/dist

# 2) 后端
cd ..
CGO_ENABLED=0 go build -trimpath -o ledger.exe ./cmd/server
CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o ledger-desktop.exe ./cmd/desktop

# 3) 安装包（installer-kit 以本目录为发布源）
cd installer-kit
go run ./build -profile v1.2 -src ..
```

> `web/dist` 不入库。若直接 `go build` 会因 `go:embed all:dist` 找不到文件失败，请先执行第 1 步（仓库内保留了 `web/dist/.gitkeep` 占位）。

### 运行

```bash
./ledger-desktop.exe                    # 桌面版
./ledger.exe                            # 浏览器版，默认 http://127.0.0.1:8770
./ledger-desktop.exe --reset-ui-cache   # 界面没更新时清理界面缓存（不动账本）
```

数据目录：`%LOCALAPPDATA%\个人记账\data`（可用 `LEDGER_DATA_DIR` 覆盖）。

---

## 关键设计

### 1. 冻结与「待确认冻结」

- 记录超过 `freeze_days`（默认 30 天）即进入冻结区，写入返回 `409 FROZEN`，改用「调整分录」。
- 导入的历史数据写 `freeze_confirmed=0`：**仍可编辑**，界面标「待确认冻结」、余额列显示 `—`。
- 物化（把历史行折进账户快照）采用**边界钳制**而非逐行跳过：

  ```
  newBoundary = min(now - freeze_days, MIN(transaction_time) WHERE freeze_confirmed = 0)
  ```

  于是待确认行永远落在动态区，`CurrentBalances` / `WindowBalances` / 校验 SQL 一行不用改，且「确认前后余额与收支完全一致」。
- 确认入口：导入结果页整批确认 / 列表筛选后批量确认 / 详情单笔确认。

### 2. 时间的两层精度

- `settings.time_precision`：**展示偏好**（全局），只影响渲染。
- `transactions.time_precision`：**行级事实**，`day` 表示该行时分秒是系统按当日分布出来的。
- 展示优先取行级：即使全局选了「秒」，系统分布的行也只显示日期并加提示，避免把占位时间伪装成真实录入。

### 3. 导入去重

- `external_id`（文件哈希 + 表 + 行号）命中 → 硬重复，唯一索引兜底。
- 内容相似（同时间 + 同金额 + 同账户 + 同商户）→ 软重复，是否跳过由 `skip_soft_duplicates` 决定，绝不静默丢弃。
- 提交后断言 `created + skipped_dup + skipped_soft_dup + skipped_invalid == 解析成功数`。

### 4. 缓存与升级

- `index.html` / SPA 路由：`Cache-Control: no-store`；带哈希的 `assets/*`：`immutable`。
- WebView2 用户数据目录按版本隔离（`data/webview2/v1.2`），避免升级后复用旧入口页导致「界面没变」。

更多细节见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)、[`docs/ARCHITECTURE-V12.md`](docs/ARCHITECTURE-V12.md)。

---

## 已知限制

| 限制 | 说明 |
| --- | --- |
| 不支持 `.xls`（BIFF8） | 请另存为 `.xlsx` / `.csv`；选择文件时会提示 |
| 未做代码签名 | 首次运行触发 SmartScreen，需「更多信息 → 仍要运行」 |
| 需 WebView2 运行时 | Win11 / Win10 1803+ 通常自带；缺失时程序给出提示，也可用 `ledger.exe` 以浏览器打开 |
| 单账户下无内部转账 | 转账需两个不同账户，先在「设置 → 账户」新增 |
| macOS / Linux 未适配 | 桌面壳与安装器依赖 Win32 API |
| OCR 需可选服务 | `ocr-service/` 未启动时该功能不可用，不影响其他功能 |

---

## 数据与隐私

- 所有数据保存在本机 SQLite，无云端、无遥测、无第三方请求（OCR 除外，且为本地服务）。
- 首次启动生成随机访问令牌，保存在本机 `meta` 表；桌面版自动注入，浏览器模式需登录一次。
- 备份为本地文件；账户合并、库结构升级前自动备份。

---

## License

MIT，详见 [LICENSE](LICENSE)。
