# installer-kit — 个人记账 Windows 安装器

自研的图形化安装/卸载程序（Go + WebView2，纯 Go 无 cgo），同时支持 **v1.0（Web 版）** 与
**v1.1（桌面版）** 两个发行版本。产物是单文件 `*.exe`：双击出图形界面，也支持全静默部署。

设计要点：

- **不要求管理员权限**：只写 `HKCU`，只装到用户目录，天然免 UAC。
- **同一份源码、两个版本**：差异集中在 `profile.go` 的配置表，编译期用 `-ldflags -X` 注入，
  避免维护两份安装器。
- **payload 可复现、二进制不一定**：`go run ./build -profile v1.0|v1.1` 每次都会重打 payload
  并重新编译。**payload.zip 的内容是确定的**（同一份源文件 → 同一个 payload sha256），
  但安装包 exe 本身的 SHA-256 **每次构建可能不同**（Go 工具链写入的构建元数据/构建 ID 差异），
  因此对外发布时应以「当次构建产物」的哈希为准，并用 payload 哈希判断载荷是否变化。

---

## 1. 目录与文件职责

| 文件 | 职责 |
| --- | --- |
| `main.go` | 入口：解析参数并分发到 图形安装 / 图形卸载 / 静默安装 / 静默卸载 / 自清理 五种模式 |
| `profile.go` | **多版本差异表**（`profileV10` / `profileV11`）与派生的产品元信息变量、数据目录定位 |
| `install.go` | 安装主流程（校验路径 → 查空间 → 解压 payload → 写 `install.json` → 生成 `uninstall.exe` → 快捷方式 → 注册表 → 启动） |
| `uninstall.go` | 卸载主流程（结束进程 → 清快捷方式 → 清注册表 → 删文件 → 可选删用户数据 → 自删除代理） |
| `gui.go` | WebView2 窗口 + 本地 HTTP 接口（`/api/state`、`/api/install`、`/api/uninstall`、`/api/log` 等） |
| `ui/` | 内嵌前端（`index.html` / `app.css` / `app.js`，无构建步骤） |
| `payload.go` | 内嵌 `payload.zip` / `payload.json` 的读取、解压、元信息校验，`install.json` 读写 |
| `registry.go` | `HKCU` 卸载信息项的写入/删除（`golang.org/x/sys/windows/registry`，不调用 `reg.exe`） |
| `shortcut.go` | 快捷方式（WScript.Shell COM）与「选择文件夹」对话框（PowerShell `-EncodedCommand`） |
| `paths.go` | 默认安装目录、桌面/开始菜单定位（读 User Shell Folders，兼容 OneDrive 重定向）、路径校验 |
| `syswin.go` | 少量 Win32 调用（单实例互斥量、磁盘可用空间、消息框）与 `%VAR%` 环境变量展开 |
| `progress.go` | 线程安全的进度/日志记录器（GUI 增量拉取，静默模式打印到 stdout） |
| `ziputil.go` | zip 解压落盘、文件复制等小工具 |
| `build/main.go` | **构建工具**：按 profile 组装 payload 并编译安装包 |
| `payload/` | 构建中间产物（`payload.zip`、`payload.json`），会被 `go:embed` 打进安装包 |
| `installer_test.go` | 单元测试（路径校验、脚本语法、zip 越界、profile 差异、数据目录位置） |

---

## 2. 快速开始

```bash
cd installer-kit

# 构建 v1.0 安装包（默认 profile）
go run ./build -profile v1.0

# 构建 v1.1 桌面版安装包
go run ./build -profile v1.1

# 只重建 payload，不编译
go run ./build -profile v1.1 -payload-only

# 复用已有 payload，只编译（会校验 payload.json 的版本与 profile 是否匹配）
go run ./build -profile v1.0 -skip-payload

# 自定义发布目录与输出路径
go run ./build -profile v1.1 -src "D:/Project/记账/记账v1.1" -out "D:/tmp/setup-v11.exe"
```

构建工具参数：

| 参数 | 说明 |
| --- | --- |
| `-profile v1.0\|v1.1` | 目标版本，默认 `v1.0` |
| `-src` | 发布目录，默认按 profile 取 `../记账v1.0` 或 `../记账v1.1` |
| `-out` | 安装包输出路径，默认 `<src>/<profile 输出名>` |
| `-payload-only` | 只重新生成 `payload/`，不编译 |
| `-skip-payload` | 复用 `payload/`，只编译 |
| `-with-plan` | 是否把项目根目录 `项目计划书.html` 打进包（**仅 v1.0 支持**，默认 `false`） |

编译期注入（构建工具自动完成，手工 `go build` 时需自行传递）：

```bash
go build -trimpath -buildvcs=false -ldflags "-s -w -H=windowsgui -X main.profileName=v1.1" -o setup.exe .
```

`v1.0` **不注入** `-X`（默认值即 `v1.0`），保持与历史构建一致。

---

## 3. 多版本 profile（v1.0 / v1.1）

差异全部集中在 `profile.go`，其它源文件只消费派生变量（`AppExeName`、`UninstallRegPath` 等）。

| 项 | v1.0（Web 版） | v1.1（桌面版） |
| --- | --- | --- |
| 显示名 / 版本 | 个人记账 v1.0 / `1.0.0` | 个人记账 v1.1 / `1.1.0` |
| 安装包输出路径 | `D:/Project/记账/记账v1.0/记账v1.0-安装包.exe` | `D:/Project/记账/记账v1.1/记账v1.1-安装包.exe` |
| 默认安装目录 | `%LOCALAPPDATA%\Programs\个人记账v1.0` | `%LOCALAPPDATA%\Programs\个人记账v1.1` |
| 主程序 | `ledger.exe` | `ledger-desktop.exe` |
| 快捷方式目标 | `<安装目录>\启动.bat` | `<安装目录>\ledger-desktop.exe` |
| 安装后自启动 | 运行 `启动.bat`（拉起后端 + 打开浏览器） | 直接运行 `ledger-desktop.exe`（独立窗口） |
| payload | `ledger.exe`、`web/dist/**`、`docs/**`、`scripts/**`、`README.md`、`启动.bat` | `ledger-desktop.exe`、`docs/**`、`README-DESKTOP.md`（**不含** `web/`、`scripts/`、`启动.bat`） |
| 卸载注册表项 | `HKCU\...\Uninstall\个人记账v1.0` | `HKCU\...\Uninstall\个人记账v1.1` |
| 卸载前结束的进程 | `ledger.exe` | `ledger-desktop.exe`（再尽力 `ledger.exe`） |
| **用户数据目录** | `<安装目录>\data`（在安装目录内） | `%LOCALAPPDATA%\个人记账\data`（**在安装目录外**） |
| 单实例互斥量 | `Local\LedgerInstallerV1` | `Local\LedgerInstallerV11` |
| 窗口标题 | `个人记账 v1.0 安装程序 / 卸载程序` | `个人记账 v1.1 安装程序 / 卸载程序` |

因为注册表键、互斥量、默认目录、WebView2 数据目录（`%TEMP%\ledger-installer-webview2-<profile>`）
都按版本区分，**两个版本可以同时安装、同时打开安装器**，互不干扰。

### payload 打包规则

- 白名单收集：只打上表列出的文件/目录，其余一律不进包（`web/node_modules`、`.git` 永远跳过）。
- 过滤过程性文档：`INSTALL-VERIFY.md`（两个版本都过滤）、`STATUS.md`（v1.1 过滤）。
- `v1.0` 缺少 `启动.bat` 时会自动生成一份（保证开箱可用）；`v1.1` 不生成启动脚本。
- `README-DESKTOP.md` 对 v1.1 是**可选文件**：缺失只告警不中断（最终交付应确保存在）。

---

## 4. 命令行参数

安装包与安装后的 `uninstall.exe` 是同一个可执行文件，靠参数区分角色。

| 参数 | 说明 |
| --- | --- |
| （无参数） | 图形安装界面 |
| `--uninstall` | 图形卸载界面（从安装目录内的 `uninstall.exe` 启动时自动定位安装目录） |
| `--silent` | 静默模式，不显示界面；成功输出 `INSTALL_OK <dir>` / `UNINSTALL_OK <dir>`，失败输出 `*_FAIL <原因>` 并以退出码 1 结束 |
| `--dir=<绝对路径>` | 指定安装/卸载目标目录。支持环境变量写法：`%LOCALAPPDATA%`（cmd 风格）、`$LOCALAPPDATA` / `${LOCALAPPDATA}`、`$env:LOCALAPPDATA`（PowerShell 风格）；未定义的变量不会被静默展开，会因「不是绝对路径」被拒绝 |
| `--no-shortcuts` | 不创建任何快捷方式 |
| `--no-desktop` | 只跳过桌面快捷方式 |
| `--no-startmenu` | 只跳过开始菜单快捷方式 |
| `--no-launch` | 安装完成后不自动启动 |
| `--remove-data` | 卸载时**同时删除用户数据**（危险，账本会丢失） |
| `--version` | 打印 `个人记账 <版本>` 后退出 |
| `--self-clean --target=<文件> --dir=<目录>` | 内部/兼容用：旧版「复制本体到 %TEMP% 再自删除」代理。**当前卸载流程已不再使用它**，改用 `%TEMP%\jz-clean-<pid>.cmd` 小脚本代理（见下） |

示例：

```bash
# 静默安装到自定义目录，不建快捷方式、装完不启动
"记账v1.1-安装包.exe" --silent --dir="D:\Apps\个人记账v1.1" --no-shortcuts --no-launch

# 静默卸载（保留用户数据）
"D:\Apps\个人记账v1.1\uninstall.exe" --uninstall --silent

# 静默卸载并删除用户数据
"D:\Apps\个人记账v1.1\uninstall.exe" --uninstall --silent --remove-data
```

---

## 5. 数据目录与 `--remove-data` 语义

| 版本 | 数据目录 | 卸载默认行为 | 加 `--remove-data` / 界面勾选 |
| --- | --- | --- | --- |
| v1.0 | `<安装目录>\data` | 保留 `data\`，其余全删（目录可能残留空壳） | 连 `data\` 一起删，安装目录随之消失 |
| v1.1 | `%LOCALAPPDATA%\个人记账\data` | **保留**（安装目录照常删干净） | 删除该目录；父目录 `%LOCALAPPDATA%\个人记账` 变空时一并移除 |

要点：

- v1.1 的数据目录**与安装位置解耦**，重装、换盘、升级都不会碰到账本。
- 图形卸载界面会显示真实数据目录路径，勾选项文案带路径，避免误删。
- 从 v1.0 升级到桌面版时，把 v1.0 的 `data\` 手动拷到 `%LOCALAPPDATA%\个人记账\` 即可（详见 `记账v1.1/README-DESKTOP.md`）。

---

## 6. 卸载注册表项（HKCU）

路径：`HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\个人记账<版本>`

写入字段：`DisplayName`、`DisplayVersion`、`Publisher`、`InstallLocation`、`DisplayIcon`、
`UninstallString`、`QuietUninstallString`、`InstallDate`、`Comments`、`URLInfoAbout`、
`NoModify=1`、`NoRepair=1`、`EstimatedSize`（KB）、`SystemComponent=0`。

卸载时先删快捷方式、再删注册表项、最后删文件并自删除，保证任何一步失败都不会留下
「注册表里还在但文件已不存在」的残项。

---

## 7. 验证清单（发布前必跑）

1. **构建**：`go vet ./...` 干净、`go test ./...` 全绿。
2. **安装**：`--silent --dir=<临时目录> --no-launch`，退出码 0；检查文件数、`install.json`
   （`version` 与 `data_dir` 正确）。
3. **健康检查**：启动主程序后 `GET http://127.0.0.1:8770/api/health` 返回 200 且
   `version` 与 profile 一致（端口被占用时从日志里取实际端口）。
4. **快捷方式**：桌面 `个人记账 v<版本>.lnk`、开始菜单主项与卸载项存在，目标文件为
   profile 指定的 `ShortcutTarget`；**卸载项必须带 `Arguments="--uninstall"`**（字节中应能搜到
   UTF-16LE 的 `--uninstall`，双击后窗口标题应为 `个人记账 v<版本> 卸载程序`）。
   COM 被策略拦截时可解析 `.lnk` 字节中的 UTF-16LE 路径。
5. **注册表**：上表字段齐全，`DisplayVersion` 与 profile 一致。
6. **卸载**：默认保留用户数据；`--remove-data` 后数据与安装目录均消失；快捷方式与注册表项清除。
7. **共存**：两个版本同时安装后卸载其一，另一版本的文件与注册表项不受影响。
8. **环境变量路径**：`--dir="%LOCALAPPDATA%\Programs\测试v11"` 应成功解析并安装到展开后的真实路径；
   `--dir="%NOPE%\x"`（未定义变量）应被拒绝并输出 `INSTALL_FAIL 请输入绝对路径`。

9. **卸载进程作用域**：v1.0 与 v1.1 同时存在且各自运行时，卸载其中一个，另一个进程必须存活
   （只结束映像路径位于本安装目录内的进程）。
10. **TEMP 清洁度**：连续 10 次安装→卸载后，`%TEMP%` 不得出现 `ledger-uninstaller-*.exe`；
   允许短暂出现 ≤1KB 的 `jz-clean-*.cmd`，并应在 5 秒内自行消失。

> ⚠️ **并行运行禁令**：快捷方式与卸载注册表项按「产品名 + 版本」定位（`个人记账 v1.1`、
> `HKCU\...\Uninstall\个人记账v1.1`），**与安装目录无关**。因此同一时间只允许一个角色
> （安装器开发 / QA）执行安装、卸载、改快捷方式、改注册表；并行运行会互相清掉对方刚创建的
> 快捷方式与注册表项，导致用例莫名作废。多人协作时请先用
> `tasklist | findstr ledger` 确认无实例，并串行使用同一台机器。

---

## 9. 冻结基线（v1.1 交付验收用）

| 文件 | 体积 | SHA-256 |
| --- | --- | --- |
| `记账v1.0/记账v1.0-安装包.exe` | 21,926,400 B | `89026b8e21b06e36d3a094541cc272b2f9e2390d762b5e2be4598450a8f046fd` |
| `记账v1.1/记账v1.1-安装包.exe` | 15,936,000 B | `129619f7cbd0c702a7d55d15381d5e037914bad1d6d0db0d849174be4b75ccdd` |

> 本环境的 Go 构建**不是字节可复现的**（同源码、同 payload 连构两次 exe 哈希不同；
> payload.zip 哈希稳定：v1.0 `673716a3…`、v1.1 `781f6635…`）。
> 因此：**任何重新构建都会让上述基线失效，必须通知 QA 换绑并重跑 B 组**。

## 10. 已知约束

- 依赖 **Microsoft Edge WebView2 运行时**（Win10/11 通常已预装）；缺失时会弹原生消息框提示，
  可用 `--silent` 绕过图形界面。
- 安装路径限制：必须是绝对路径、长度 ≤ 200 字符、不能是盘符根目录、不能落在
  `C:\Windows` / `C:\Program Files`（含 x86）等受保护目录下、不能含 `<>"|?*` 或多余冒号。
- 快捷方式与「选择文件夹」对话框通过 PowerShell 完成；若企业策略禁止 `powershell.exe`，
  安装会失败（此时可改用 `--no-shortcuts`）。
  **注意：卸载侧的快捷方式与注册表清理已改为 Go 原生**（`os.Remove/RemoveAll`、
  `registry.DeleteKey`），不再依赖 PowerShell——Windows 首次调用 PowerShell 会向 stderr 打
  CLIXML（"正在准备首次使用模块"）并非 0 退出，早期实现因此静默跳过清理，导致"卸载后开始菜单
  与『程序和功能』条目仍残留"。仅"结束进程"仍用 PowerShell（需要 `$_.Path` 判断路径），
  失败时降级到 `wmic` 原生兜底，只记 Warn，绝不按镜像名无差别杀进程。
- 单实例互斥量是**按用户**的：两个不同安装目录的同一版本（如两个 v1.1）不能同时运行，
  第二个实例会立即退出并聚焦已有窗口。
- `%TEMP%\ledger-installer-webview2-<profile>` 是安装器界面自身的 WebView2 缓存，可安全删除。
- 卸载时的自删除由 **`%TEMP%\jz-clean-<pid>.cmd`**（约 0.4 KB，纯 ASCII）完成：轮询删除
  `<安装目录>\uninstall.exe`、按需清理安装目录、最后删除自身。**不再把 15–22 MB 的安装包
  复制到 %TEMP%**，因此 TEMP 里不会出现 `ledger-uninstaller-*.exe` 大文件残留。
- 卸载只结束「可执行文件位于本安装目录内」的进程（按 `$_.Path` 前缀匹配）。因此 v1.0 与 v1.1
  同时安装时，卸载一个不会杀掉另一个正在运行的实例；读取不到映像路径的进程一律跳过，绝不无差别 taskkill。
