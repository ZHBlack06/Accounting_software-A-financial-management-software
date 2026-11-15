# 个人记账软件 v1.0 — 系统设计文档（ARCHITECTURE）

| 项 | 内容 |
|---|---|
| 文档版本 | v1.0 |
| 上游输入 | `D:/Project/记账/项目计划书.html`（v1.0 Final，栏目 1–10 全部定稿） |
| 本文档定位 | 技术落地设计，**不再变更产品需求**；工程师可据此直接编码 |
| 工作目录 | `D:/Project/记账` |
| 读者 | 后端工程师、前端工程师、OCR/部署工程师、QA |

---

## 0. 阅读指引

| 你要做的事 | 读哪几节 |
|---|---|
| 建库、写 model | §3 数据库设计、§4 数据模型 |
| 写 handler | §5 API 契约、§10 共享约定 |
| 写余额/调整逻辑 | §6 核心算法（**必须逐行照做**） |
| 写前端页面 | §7 前端设计 |
| 写 OCR 服务 | §8 OCR 服务设计 |
| 打包部署 | §9 部署与运维 |
| 认领任务 | `docs/TASKS.md` |

**技术约束（不可协商）**

1. 后端 Go（`go.mod` 写 `go 1.22`，本机工具链 go1.27.2 向上兼容），模块名 `ledger`。
2. Gin v1.10.0 + GORM v1.25.12 + **纯 Go SQLite 驱动 `github.com/glebarez/sqlite` v1.11.0**。`CGO_ENABLED=0` 必须能编译运行，任何方案不得依赖 cgo。
3. 后端单二进制，监听 `127.0.0.1:8770`；静态托管 `web/dist` 到 `/`，API 前缀 `/api`。
4. 前端 Vue 3 + TS + Vite + Element Plus + ECharts + Pinia；开发端口 5173，`/api` 代理到 8770。
5. OCR：Python 3.13 + FastAPI，`127.0.0.1:8771`，模型热切换，无模型时优雅降级（**主流程不得依赖联网下载**）。
6. 金额一律 `*_cents` 整数（分），API 表层同样传分，禁用浮点。
7. DB 存 UTC 秒级 `INTEGER`；API 表层用 `YYYY-MM-DD HH:mm:ss`（Asia/Shanghai）字符串，转换只在后端做。

---

## 1. 实现方案

### 1.1 关键技术难点与解法

| 难点 | 本质 | 解法 |
|---|---|---|
| **冻结区/动态区余额一致性** | 历史不可重算，未来要能自由改 | 滚动 30 天物化 + 动态区窗口累计；物化边界 `last_frozen_at` 与写保护边界 `freeze_boundary = now-30d` 分离（物化每天跑一次，滞后于写保护边界是允许的）。见 §6.4 |
| **同秒多笔的稳定排序** | 4 位月度序列会重置，不能跨月排序 | 引入 `created_seq`（全局单调递增，取自 `meta['seq:global']`，`BEGIN IMMEDIATE` 内原子递增），排序键统一 `(transaction_time, created_seq)` |
| **月度序列原子递增** | 并发插入会重号 | `meta['seq:YYYYMM']` 存"下一个待分配值"，在 `BEGIN IMMEDIATE` 写事务内先读后加一；业务 ID 冲突时按 §6.1 重试 |
| **账户分表/账本分表 = SQL 视图** | SQLite 视图不可参数化 | **双轨**：①启动时/账户创建时物化出真实 `v_account_<id>`、`v_ledger_<id>` 视图（满足 PRD 语义、供外部 SQL 工具与审计）；②**所有 API 查询走应用层参数化 SQL**（`WHERE account_id = ?`），不走视图名拼接。详见 §3.6 |
| **冻结区旧账修正** | 不能改历史，又要改对余额 | 调整分录（补差额，1 笔）/ 反冲重录（冲销+重录，2 笔）；时间强制 `now`，`related_transaction_id` 溯源；内部转账调整双边净 0。见 §6.6、§6.7 |
| **CSV 两套账单格式** | 微信/支付宝列名、编码、正负号规则不同 | `csvparse` 包：先探测（BOM/表头指纹/编码 GBK 转 UTF-8），再按 profile 走字段映射；解析结果落临时批次文件，预览→确认→提交两阶段 |
| **CSS 无 cgo 的 SQLite 并发** | WAL + 单写 | `_pragma=journal_mode(WAL)`、`busy_timeout(10000)`、`synchronous(NORMAL)`；GORM 连接池 `MaxOpenConns=1`（纯 Go 驱动下多连接写会锁竞争，单写更稳；读也走同连接，个人场景足够） |
| **OCR 模型不确定** | 不能硬绑定某个模型 | `engines/` 策略模式 + 注册表，`mock` 引擎兜底；`POST /api/v1/model/switch` 热切换；主流程无模型时返回降级结果并标 `need_review=true` |

### 1.2 框架与库选型

**后端**

| 用途 | 选型 | 理由 |
|---|---|---|
| HTTP | `github.com/gin-gonic/gin v1.10.0` | PRD 指定，中间件生态全 |
| ORM | `gorm.io/gorm v1.25.12` | 自动迁移兜底 + 链式查询；**热点 SQL 手写**（余额/统计）以保证精确 |
| SQLite | `github.com/glebarez/sqlite v1.11.0` | 纯 Go（底层 modernc.org/sqlite），CGO_ENABLED=0 可编译 |
| 参数校验 | 标准库 `net/http` + 手写 `binding` tag | 避免引入 validator 版本冲突（Gin 已内嵌 go-playground/validator，直接用 `binding:"required"`） |
| 日志 | `log/slog`（标准库） | 零依赖，JSON 输出 |
| 打包 | `archive/zip`（标准库） | PRD 指定 |
| 压缩图片 | **不压缩**（基础版直存原图 + 复制一份标记为 compressed 目录） | 引入 `imaging` 会拖慢编译；PRD 允许"压缩仅后续存储时进行"。T07 预留 `Compressor` 接口，基础版实现为字节复制 |

**前端**

| 用途 | 选型 | 说明 |
|---|---|---|
| 框架 | `vue@^3.5` + `typescript@^5.6` | — |
| 构建 | `vite@^5.4` + `@vitejs/plugin-vue` | 开发端口 5173，`server.proxy['/api'] → http://127.0.0.1:8770` |
| UI | `element-plus@^2.8`（**全量引入**） | 全量引入换取开发效率与 dark 主题一致性，包体经 gzip 后 ~350KB，桌面端本地部署可接受。按需引入需额外配 `unplugin-vue-components`，收益不抵复杂度 |
| 图表 | `echarts@^5.5`（**全量引入** + `vue-echarts` 不用，直接 `echarts.init`） | PRD 明确"预留其他图表库接入" → 用 `BaseChart.vue` 包一层 |
| 状态 | `pinia@^2.2` | — |
| 路由 | `vue-router@^4.4` | history 模式，后端需 SPA fallback |
| HTTP | `axios@^1.7` | 拦截器统一注入 Bearer + 统一错误体处理 |
| 样式 | 原生 CSS 变量 + SCSS（**不引入 Tailwind**） | PRD 要求深色模式与图表配色统一，CSS 变量比 utility class 更好维护；Element Plus 自带 dark 变量可直接复用 |
| 图标 | `@element-plus/icons-vue` | — |

> **注意**：团队默认栈是 Tailwind，但本项目 PRD 要求"深色模式 + 图表配色统一 + Element Plus 主题"，CSS 变量方案代码量更小、主题切换无闪烁。此处为有意偏离，已在 §11 记录。

**OCR**

`fastapi` + `uvicorn` + `pydantic v2` + `httpx`（调 Ollama/OpenAI 兼容端点）+ `pillow`（预处理）+ `python-multipart`。**不引入** torch/transformers（体积与下载问题），推理交给 Ollama HTTP。

### 1.3 架构模式

- 后端：**分层 + 依赖倒置**。`http → service → store`，`store` 只依赖 `*gorm.DB`/`*sql.DB`，`service` 不感知 Gin。所有跨表写操作在 `store.Tx(func)` 里完成。
- 余额计算是**唯一真相源**：任何写入交易/调整的路径必须调用 `service.Balance` 的校验钩子，禁止 handler 直接写 `transactions`。
- 前端：**容器/展示分离 + Pinia 单仓多 store**。网络请求全部收口到 `src/api/*.ts`，组件不直接 `axios`。
- 跨进程：Go ⇄ Python 仅 HTTP REST（PRD 9.4 T15）。

---

## 2. 文件清单

### 2.1 后端（47 个文件，含 go.mod/go.sum）

```
ledger/
├── go.mod                                   # 模块 ledger，go 1.22
├── go.sum
├── cmd/server/main.go                       # 入口：装配 config/store/service/http，启动物化 + 定时任务   ~120
├── internal/config/config.go                # 端口/数据目录/时区/OCR 地址/freeze_days 默认值            ~90
├── internal/store/
│   ├── db.go                                # Open(dsn)、PRAGMA、连接池、Tx helper                      ~110
│   ├── schema.sql                           # 全量 DDL（go:embed）                                     ~260
│   ├── migrate.go                           # 建表/索引/触发器/基础视图 + schema_version 升级            ~160
│   ├── seed.go                              # 分类/标签/商户/地区/账户/账本种子                          ~220
│   ├── view.go                              # v_account_<id> / v_ledger_<id> 动态生成/重建/删除           ~180
│   ├── models.go                            # GORM struct（Transaction/Account/Category/...）           ~300
│   ├── meta_repo.go                         # meta 读写 + seq 原子分配 + defaults                       ~120
│   ├── tx_repo.go                           # transactions 查询/写入/软删/物化更新                       ~420
│   ├── master_repo.go                       # accounts/categories/tags/merchants/regions/ledgers CRUD    ~380
│   └── query_builder.go                     # 交易过滤条件 → SQL where/args（唯一构造点）                ~200
├── internal/service/
│   ├── balance.go                           # 当前余额、区间余额、衔接校验、全量校验                     ~260
│   ├── materialize.go                       # 物化循环、边界推进、强制重物化                             ~240
│   ├── transaction.go                       # 创建/更新/删除/恢复/彻底删除/去重/转账双边                 ~420
│   ├── adjust.go                            # adjust / reverse / 转账双边调整                           ~260
│   ├── stats.go                             # summary/trend/breakdown/balance-series/ranking/high-amount ~380
│   ├── report.go                            # category/account/tag/period-compare                      ~300
│   ├── import.go                            # CSV 预览批次/提交入库                                     ~320
│   ├── backup.go                            # wal_checkpoint + zip 打包/列表/恢复/保留清理               ~200
│   ├── ocr.go                               # 转发 Python、任务状态代理、截图落盘                        ~220
│   ├── screenshot.go                        # 目录规划、保存、清理（30/150 天）                          ~150
│   └── maintenance.go                       # 启动任务：物化/周备份/清理/校验                            ~160
├── internal/http/
│   ├── router.go                            # 路由装配 + 静态托管 + SPA fallback                        ~200
│   ├── middleware.go                        # auth/recovery/logger/cors/error                          ~130
│   ├── response.go                          # OK/Created/Err/FROZEN 等响应助手                          ~110
│   ├── dto.go                               # 请求/响应 DTO + 映射函数（含时区转换）                     ~420
│   ├── handlers_meta.go                     # /meta /auth /settings                                     ~140
│   ├── handlers_account.go                  # accounts CRUD + archive                                  ~180
│   ├── handlers_master.go                   # categories/tags/merchants/regions                        ~280
│   ├── handlers_ledger.go                   # ledgers + rules + 视图重建                                ~200
│   ├── handlers_transaction.go              # 交易 CRUD + adjust + restore/purge                        ~340
│   ├── handlers_stats.go                    # /stats/*                                                  ~180
│   ├── handlers_report.go                   # /reports/*                                                ~150
│   ├── handlers_import.go                   # /import/csv/preview、commit                               ~180
│   ├── handlers_ocr.go                      # /ocr/*                                                    ~160
│   ├── handlers_backup.go                   # /backup/*                                                 ~120
│   └── handlers_system.go                   # /health /screenshots /templates                           ~120
└── internal/pkg/
    ├── ids/ids.go                           # 19 位业务 ID + 短 ID + 校验码                             ~110
    ├── money/money.go                       # 分/元转换与格式化（仅展示用）                              ~50
    ├── timeutil/timeutil.go                 # UTC⇄本地字符串、日/周/月/年分桶                            ~180
    ├── apierr/apierr.go                     # 错误码枚举 + Error 类型                                  ~90
    └── csvparse/
        ├── detect.go                        # 编码探测 + profile 识别                                   ~120
        ├── common.go                        # 行模型、金额/时间解析、指纹、去重键                        ~180
        ├── wechat.go                        # 微信账单 profile                                          ~140
        └── alipay.go                        # 支付宝账单 profile                                        ~140
```

> 单文件上限 ~600 行，全部满足，可多人并行。

### 2.2 前端（85 个文件）

```
web/
├── package.json                      ├── vite.config.ts                ├── tsconfig.json
├── tsconfig.node.json                ├── index.html                    ├── env.d.ts
├── src/main.ts                       ├── src/App.vue                   ├── src/router/index.ts
├── src/styles/vars.css               # 亮/暗主题变量（图表配色同源）
├── src/styles/index.scss             # 全局 reset + Element Plus 变量覆盖
├── src/api/client.ts                 # axios 实例 + 拦截器 + 统一错误体解析
├── src/api/types.ts                  # 全部 DTO 的 TS 类型（与 §5 一一对应）
├── src/api/meta.ts  auth.ts  accounts.ts  categories.ts  tags.ts  merchants.ts
├── src/api/regions.ts  ledgers.ts  transactions.ts  stats.ts  reports.ts
├── src/api/imports.ts  ocr.ts  backup.ts  system.ts
├── src/stores/ui.ts                  # 主题、时间范围预设、筛选偏好、图表类型
├── src/stores/auth.ts                # token、登录态
├── src/stores/master.ts              # accounts/categories/tags/merchants/regions/ledgers 缓存
├── src/stores/transaction.ts         # 列表分页、查询条件、详情抽屉、编辑态
├── src/stores/stats.ts               # summary/trend/breakdown/series/ranking/highAmount
├── src/stores/draft.ts               # 快速记账默认值记忆 + 模板
├── src/utils/money.ts                # 分→"¥1,234.56"、输入解析
├── src/utils/datetime.ts             # 预设区间计算、本地格式化
├── src/utils/presets.ts              # 时间范围预设枚举 + 计算
├── src/utils/echarts.ts              # echarts 按需注册 + 主题/调色板
├── src/layouts/AppLayout.vue         # 三段式骨架 + 侧边/抽屉菜单 + 快捷键
├── src/components/common/
│   ├── BaseChart.vue  MoneyText.vue  StatCard.vue  TimeRangePicker.vue
│   ├── FilterTagBar.vue  PageHeader.vue  ConfirmDialog.vue  EmptyState.vue
├── src/components/transaction/
│   ├── TransactionTable.vue  TransactionDetailDrawer.vue  TransactionEditDialog.vue
│   ├── TransactionFilters.vue  AdjustDialog.vue  TransactionItemEditor.vue  TagSelect.vue
├── src/components/entry/
│   ├── QuickEntryDialog.vue  TransactionForm.vue  TransferSection.vue  TemplateBar.vue
├── src/components/charts/
│   ├── TrendChart.vue  CategoryPieChart.vue  AccountBalanceChart.vue
│   ├── CategoryRankingList.vue  HighAmountList.vue
├── src/components/reports/
│   ├── CategoryReport.vue  AccountReport.vue  TagReport.vue  PeriodCompareReport.vue
├── src/components/import/
│   ├── CsvImportPanel.vue  CsvPreviewTable.vue  OcrUploadPanel.vue  OcrResultCard.vue
├── src/components/settings/
│   ├── AccountSettings.vue  CategorySettings.vue  TagSettings.vue  MerchantSettings.vue
│   ├── LedgerSettings.vue  LedgerRuleEditor.vue  SystemSettings.vue  BackupSettings.vue
└── src/views/
    ├── DashboardView.vue  TransactionsView.vue  ReportsView.vue
    ├── ImportView.vue  SettingsView.vue  LoginView.vue
```

> 计：根 6 + main/App/router 3 + styles 2 + api 17 + stores 6 + utils 4 + layout 1 + components 8+7+4+5+4+4+8=40 + views 6 = **85**。

### 2.3 OCR 服务（21 个文件）

```
ocr-service/
├── requirements.txt  Dockerfile  README.md
├── app/main.py                     # FastAPI 装配 + lifespan 预热
├── app/config.py                   # 模型名/地址/超时/并发/日志级别（env 覆盖）
├── app/schemas.py                  # 请求/响应 pydantic 模型（与 PRD 7.3 字段一致）
├── app/deps.py                     # 引擎单例注入
├── app/routers/health.py           # /healthz /readyz
├── app/routers/recognize.py        # /api/v1/recognize、/batch
├── app/routers/tasks.py            # /api/v1/tasks/{id}
├── app/routers/model.py            # /api/v1/model、/api/v1/model/switch
├── app/engines/base.py             # BaseEngine 抽象（recognize/health/load/unload）
├── app/engines/registry.py         # 名称→引擎注册表 + 当前引擎 + 切换锁
├── app/engines/mock.py             # 无模型兜底（正则+版面启发式，输出 need_review=true）
├── app/engines/ollama.py           # Ollama /api/chat 多模态调用
├── app/engines/openai_vl.py        # OpenAI 兼容 /v1/chat/completions（vLLM/LMStudio）
├── app/pipeline/preprocess.py      # 缩放/EXIF 纠正/灰度/二值
├── app/pipeline/prompt.py          # 结构化提取提示词（微信/支付宝两版）
├── app/pipeline/postprocess.py     # JSON 修复、字段规整、单位换算
├── app/pipeline/rules.py           # PRD 7.6 七条规则校验
├── app/store/tasks.py              # 内存任务表（TTL 1h）+ 进度
└── models/README.md                # 模型放置与热切换说明（不入库）
```

### 2.4 部署与脚本（9 个文件）

```
docker-compose.yml        Dockerfile.backend      Dockerfile.web
scripts/dev-backend.ps1   scripts/dev-frontend.ps1
scripts/build.ps1         scripts/start.ps1       scripts/e2e-smoke.ps1
README.md
```

**合计 47 + 85 + 21 + 9 = 162 个文件。**

---

## 3. 数据库设计

### 3.1 连接与 PRAGMA

DSN（`internal/store/db.go`）：

```go
dsn := "data/ledger.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
    Logger: logger.Default.LogMode(logger.Silent), // 用 slog 自己记
    NowFunc: func() time.Time { return time.Now().UTC() },
    SkipDefaultTransaction: true,                  // 我们自己控事务
})
sqlDB, _ := db.DB()
sqlDB.SetMaxOpenConns(1)   // 纯 Go 驱动 + 单写者，避免 SQLITE_BUSY
sqlDB.SetMaxIdleConns(1)
sqlDB.SetConnMaxLifetime(0)
```

启动后额外执行一次 `PRAGMA wal_autocheckpoint=1000;`。

### 3.2 全量 DDL（`internal/store/schema.sql`）

```sql
-- ============ 元数据 / 计数器 ============
CREATE TABLE IF NOT EXISTS meta (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);

-- ============ 账户 ============
CREATE TABLE IF NOT EXISTS accounts (
  id                    TEXT PRIMARY KEY,                          -- 'acc_' + 8 位小写 base32
  name                  TEXT NOT NULL,                            -- '招商银行借记卡1'
  alias                 TEXT NOT NULL DEFAULT '',
  account_type          TEXT NOT NULL DEFAULT 'asset'
                          CHECK (account_type IN ('asset')),
  value_type            TEXT NOT NULL DEFAULT 'wallet'             -- 储值类型
                          CHECK (value_type IN ('wallet','wealth','yuebao','current','fixed','other')),
  bank_name             TEXT NOT NULL DEFAULT '',
  card_tail             TEXT NOT NULL DEFAULT ''
                          CHECK (card_tail = '' OR length(card_tail) = 4),
  currency              TEXT NOT NULL DEFAULT 'CNY',
  initial_balance_cents INTEGER NOT NULL DEFAULT 0,
  initial_balance_time  INTEGER NOT NULL DEFAULT 0,               -- UTC 秒，初始余额起算点
  is_count_in_total     INTEGER NOT NULL DEFAULT 1 CHECK (is_count_in_total IN (0,1)),
  sort_order            INTEGER NOT NULL DEFAULT 0,
  is_archived           INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  archived_at           INTEGER,
  remark                TEXT NOT NULL DEFAULT '',
  created_at            INTEGER NOT NULL,
  updated_at            INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_accounts_sort ON accounts(is_archived, sort_order);

-- ============ 分类（两级） ============
CREATE TABLE IF NOT EXISTS categories (
  id               TEXT PRIMARY KEY,                              -- 'cat_' + 8
  name             TEXT NOT NULL,
  parent_id        TEXT REFERENCES categories(id) ON DELETE RESTRICT,
  level            INTEGER NOT NULL DEFAULT 1 CHECK (level IN (1,2)),
  transaction_type TEXT NOT NULL DEFAULT 'expense'
                     CHECK (transaction_type IN ('expense','income','both')),
  is_system        INTEGER NOT NULL DEFAULT 0,
  is_archived      INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  color            TEXT NOT NULL DEFAULT '',
  icon             TEXT NOT NULL DEFAULT '',
  sort_order       INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_categories_name
  ON categories(IFNULL(parent_id,''), name);
CREATE INDEX IF NOT EXISTS ix_categories_parent ON categories(parent_id, sort_order);

-- ============ 标签 ============
CREATE TABLE IF NOT EXISTS tags (
  id          TEXT PRIMARY KEY,                                   -- 'tag_' + 8
  name        TEXT NOT NULL UNIQUE,
  color       TEXT NOT NULL DEFAULT '',
  sort_order  INTEGER NOT NULL DEFAULT 0,
  use_count   INTEGER NOT NULL DEFAULT 0,
  is_archived INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS transaction_tags (
  transaction_id TEXT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
  tag_id         TEXT NOT NULL REFERENCES tags(id) ON DELETE RESTRICT,
  PRIMARY KEY (transaction_id, tag_id)
);
CREATE INDEX IF NOT EXISTS ix_tt_tag ON transaction_tags(tag_id, transaction_id);

-- ============ 商户 / 平台 ============
CREATE TABLE IF NOT EXISTS merchants (
  id            TEXT PRIMARY KEY,                                 -- 'mch_' + 8
  name          TEXT NOT NULL UNIQUE,
  alias         TEXT NOT NULL DEFAULT '',
  merchant_type TEXT NOT NULL DEFAULT 'offline'
                  CHECK (merchant_type IN ('offline','online')),
  platform_code TEXT NOT NULL DEFAULT '',                         -- taobao/jd/pdd/...
  region_id     TEXT,
  use_count     INTEGER NOT NULL DEFAULT 0,
  is_system     INTEGER NOT NULL DEFAULT 0,
  is_archived   INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_merchants_type ON merchants(merchant_type, is_archived, name);

-- ============ 地区（县级以上行政区划字典） ============
CREATE TABLE IF NOT EXISTS regions (
  code        TEXT PRIMARY KEY,                                   -- 6 位行政区划，'000000'=全国
  name        TEXT NOT NULL,                                      -- '静安区'
  full_name   TEXT NOT NULL,                                      -- '上海市静安区'
  parent_code TEXT NOT NULL DEFAULT '',
  level       INTEGER NOT NULL,                                   -- 1省 2市 3县区
  pinyin      TEXT NOT NULL DEFAULT '',
  pinyin_abbr TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS ix_regions_parent ON regions(parent_code);
CREATE INDEX IF NOT EXISTS ix_regions_pinyin ON regions(pinyin, pinyin_abbr);

-- ============ 账本 + 绑定规则 ============
CREATE TABLE IF NOT EXISTS ledgers (
  id          TEXT PRIMARY KEY,                                   -- 'ldg_' + 8
  name        TEXT NOT NULL UNIQUE,
  currency    TEXT NOT NULL DEFAULT 'CNY',
  remark      TEXT NOT NULL DEFAULT '',
  is_default  INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0,1)),
  sort_order  INTEGER NOT NULL DEFAULT 0,
  is_archived INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0,1)),
  archived_at INTEGER,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ledger_rules (
  id         TEXT PRIMARY KEY,                                    -- 'rule_' + 8
  ledger_id  TEXT NOT NULL REFERENCES ledgers(id) ON DELETE CASCADE,
  match_mode TEXT NOT NULL DEFAULT 'or' CHECK (match_mode IN ('or','all')),
  is_active  INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0,1)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_rules_ledger ON ledger_rules(ledger_id);

CREATE TABLE IF NOT EXISTS ledger_rule_targets (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id     TEXT NOT NULL REFERENCES ledger_rules(id) ON DELETE CASCADE,
  target_type TEXT NOT NULL
                CHECK (target_type IN ('category_l1','category_l2','tag','account','merchant')),
  target_id   TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_lrt
  ON ledger_rule_targets(rule_id, target_type, target_id);
CREATE INDEX IF NOT EXISTS ix_lrt_target ON ledger_rule_targets(target_type, target_id);

-- ============ 交易总表（唯一真相源） ============
CREATE TABLE IF NOT EXISTS transactions (
  id                     TEXT PRIMARY KEY CHECK (length(id) = 19),
  created_seq            INTEGER NOT NULL UNIQUE,                 -- 全局自增排序键
  transaction_time       INTEGER NOT NULL,                        -- UTC 秒
  local_date             TEXT NOT NULL,                           -- 'YYYY-MM-DD'（Asia/Shanghai）
  transaction_type       TEXT NOT NULL
                           CHECK (transaction_type IN ('expense','income','transfer')),
  amount_cents           INTEGER NOT NULL CHECK (amount_cents > 0),
  currency               TEXT NOT NULL DEFAULT 'CNY',
  fx_rate                REAL NOT NULL DEFAULT 1,
  account_id             TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
  transfer_direction     TEXT CHECK (transfer_direction IN ('out','in')),
  transfer_group_id      TEXT,
  counter_account_id     TEXT REFERENCES accounts(id) ON DELETE RESTRICT,
  category_id            TEXT REFERENCES categories(id) ON DELETE RESTRICT,
  sub_category_id        TEXT REFERENCES categories(id) ON DELETE RESTRICT,
  merchant_id            TEXT REFERENCES merchants(id) ON DELETE SET NULL,
  merchant_text          TEXT NOT NULL DEFAULT '',                -- 未入库的临时商户名
  region_id              TEXT REFERENCES regions(code) ON DELETE SET NULL,
  counterparty_name      TEXT NOT NULL DEFAULT '',
  counterparty_account   TEXT NOT NULL DEFAULT '',
  remark                 TEXT NOT NULL DEFAULT '',
  ocr_raw_text           TEXT NOT NULL DEFAULT '',
  screenshot_id          TEXT NOT NULL DEFAULT '',
  is_adjustment          INTEGER NOT NULL DEFAULT 0 CHECK (is_adjustment IN (0,1)),
  adjustment_type        TEXT CHECK (adjustment_type IN ('adjust','reverse')),
  related_transaction_id TEXT,
  source                 TEXT NOT NULL DEFAULT 'manual'
                           CHECK (source IN ('manual','csv','ocr','adjustment','import')),
  external_id            TEXT NOT NULL DEFAULT '',                -- 账单流水号/CSV 行指纹
  account_balance_after  INTEGER,                                 -- 物化结果（冻结区只读）
  total_balance_after    INTEGER,
  materialized_at        INTEGER,
  deleted_at             INTEGER,                                 -- 软删除
  created_at             INTEGER NOT NULL,
  updated_at             INTEGER NOT NULL,
  CHECK (transaction_type <> 'transfer'
         OR (transfer_group_id IS NOT NULL AND transfer_direction IS NOT NULL
             AND counter_account_id IS NOT NULL)),
  CHECK (is_adjustment = 0
         OR (adjustment_type IS NOT NULL AND related_transaction_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS ix_tx_order        ON transactions(transaction_time, created_seq);
CREATE INDEX IF NOT EXISTS ix_tx_account_time ON transactions(account_id, transaction_time, created_seq);
CREATE INDEX IF NOT EXISTS ix_tx_cat_time     ON transactions(category_id, transaction_time);
CREATE INDEX IF NOT EXISTS ix_tx_subcat_time  ON transactions(sub_category_id, transaction_time);
CREATE INDEX IF NOT EXISTS ix_tx_merchant     ON transactions(merchant_id, transaction_time);
CREATE INDEX IF NOT EXISTS ix_tx_region       ON transactions(region_id);
CREATE INDEX IF NOT EXISTS ix_tx_tgroup       ON transactions(transfer_group_id);
CREATE INDEX IF NOT EXISTS ix_tx_related      ON transactions(related_transaction_id);
CREATE INDEX IF NOT EXISTS ix_tx_adjust       ON transactions(is_adjustment, transaction_time);
CREATE INDEX IF NOT EXISTS ix_tx_deleted      ON transactions(deleted_at);
CREATE INDEX IF NOT EXISTS ix_tx_local_date   ON transactions(local_date);
CREATE INDEX IF NOT EXISTS ix_tx_source       ON transactions(source);
CREATE UNIQUE INDEX IF NOT EXISTS ux_tx_external
  ON transactions(external_id) WHERE external_id <> '';

-- ============ 多物品明细 ============
CREATE TABLE IF NOT EXISTS transaction_items (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  transaction_id        TEXT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
  name                  TEXT NOT NULL DEFAULT '',
  sub_category_id       TEXT REFERENCES categories(id) ON DELETE SET NULL,
  original_amount_cents INTEGER NOT NULL DEFAULT 0 CHECK (original_amount_cents >= 0),
  paid_amount_cents     INTEGER NOT NULL DEFAULT 0 CHECK (paid_amount_cents >= 0),
  discount_cents        INTEGER GENERATED ALWAYS AS
                          (original_amount_cents - paid_amount_cents) VIRTUAL,
  discount_reason       TEXT NOT NULL DEFAULT '',
  sort_order            INTEGER NOT NULL DEFAULT 0,
  created_at            INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_items_tx ON transaction_items(transaction_id, sort_order);

-- ============ 截图与 OCR ============
CREATE TABLE IF NOT EXISTS screenshots (
  id               TEXT PRIMARY KEY,                              -- 19 位 ID（与交易 ID 同源）
  transaction_id   TEXT,
  original_path    TEXT NOT NULL,
  compressed_path  TEXT NOT NULL DEFAULT '',
  ocr_result_path  TEXT NOT NULL DEFAULT '',
  ocr_status       TEXT NOT NULL DEFAULT 'pending'
                     CHECK (ocr_status IN ('pending','processing','done','failed','skipped')),
  ocr_source       TEXT NOT NULL DEFAULT 'pc_local',
  ocr_model        TEXT NOT NULL DEFAULT '',
  ocr_elapsed_ms   INTEGER NOT NULL DEFAULT 0,
  need_review      INTEGER NOT NULL DEFAULT 0 CHECK (need_review IN (0,1)),
  file_size        INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  deleted_at       INTEGER
);
CREATE INDEX IF NOT EXISTS ix_ss_status ON screenshots(ocr_status, created_at);
CREATE INDEX IF NOT EXISTS ix_ss_tx ON screenshots(transaction_id);
```

### 3.3 基础视图（迁移时创建，静态）

```sql
DROP VIEW IF EXISTS v_transactions_signed;
CREATE VIEW v_transactions_signed AS
SELECT
  t.*,
  CASE
    WHEN t.transaction_type = 'expense'  THEN -t.amount_cents
    WHEN t.transaction_type = 'income'   THEN  t.amount_cents
    WHEN t.transfer_direction = 'out'    THEN -t.amount_cents
    WHEN t.transfer_direction = 'in'     THEN  t.amount_cents
    ELSE 0
  END AS signed_cents,
  CASE WHEN t.is_adjustment = 1 THEN
    CASE
      WHEN t.transaction_type = 'expense' THEN -t.amount_cents
      WHEN t.transaction_type = 'income'  THEN  t.amount_cents
      WHEN t.transfer_direction = 'out'   THEN -t.amount_cents
      WHEN t.transfer_direction = 'in'    THEN  t.amount_cents
      ELSE 0
    END
  ELSE 0 END AS adjustment_signed_cents,
  CASE WHEN t.transaction_type = 'expense' AND t.is_adjustment = 0
       THEN t.amount_cents ELSE 0 END AS stat_expense_cents,
  CASE WHEN t.transaction_type = 'income' AND t.is_adjustment = 0
       THEN t.amount_cents ELSE 0 END AS stat_income_cents
FROM transactions t
WHERE t.deleted_at IS NULL;
```

> `v_transactions_signed` 是**所有余额与统计 SQL 的唯一入口**，避免各处重复写符号逻辑。

### 3.4 触发器

```sql
-- updated_at 自动维护（accounts/categories/tags/merchants/ledgers）
CREATE TRIGGER IF NOT EXISTS trg_accounts_updated AFTER UPDATE ON accounts
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN UPDATE accounts SET updated_at = CAST(strftime('%s','now') AS INTEGER) WHERE id = NEW.id; END;
-- （categories/tags/merchants/ledgers 同构，略）

-- 标签使用次数
CREATE TRIGGER IF NOT EXISTS trg_tt_ins AFTER INSERT ON transaction_tags
FOR EACH ROW BEGIN
  UPDATE tags SET use_count = use_count + 1 WHERE id = NEW.tag_id;
END;
CREATE TRIGGER IF NOT EXISTS trg_tt_del AFTER DELETE ON transaction_tags
FOR EACH ROW BEGIN
  UPDATE tags SET use_count = use_count - 1 WHERE id = OLD.tag_id AND use_count > 0;
END;

-- 禁止物理删除已被引用的账户（错误码落在 SQLite 层，服务层会先给出友好错误）
CREATE TRIGGER IF NOT EXISTS trg_accounts_no_delete BEFORE DELETE ON accounts
FOR EACH ROW
WHEN EXISTS (SELECT 1 FROM transactions WHERE account_id = OLD.id)
BEGIN SELECT RAISE(ABORT, 'ACCOUNT_IN_USE'); END;
```

> **不**用触发器做冻结区写保护（需要 `now`，SQLite 无法稳定获取且难测试），统一由 `service.Balance` 判定并返回结构化 409。

### 3.5 索引与查询对应表

| 查询 | 走的索引 |
|---|---|
| 交易列表按时间分页 | `ix_tx_order` |
| 账户视图/账户流水 | `ix_tx_account_time` |
| 分类占比 | `ix_tx_cat_time` |
| 标签过滤 | `ix_tt_tag` → `transactions` PK |
| 转账成对查找 | `ix_tx_tgroup` |
| 调整溯源 | `ix_tx_related` |
| 导入去重（时间+金额+账户） | `ix_tx_account_time` 前缀 + 过滤 |

### 3.6 账户分表 / 账本分表 = SQL 视图（落地决策）

**结论：双轨实现 —— 真实视图（物化 DDL）+ 应用层参数化查询。API 一律走参数化查询，视图仅供外部 SQL 工具 / 审计 / 用户自查使用。**

**理由**

1. SQLite 视图定义里**不能带参数**，`SELECT * FROM v_account_?` 无解；视图名动态拼接会引入注入面。
2. 视图名与 id 强绑定，账户改名/归档时视图语义不变（用内部 id，非中文名），但**账户数量增长会累积大量视图**（个人 10–30 个，可接受）。
3. 应用层参数化查询走 `ix_tx_account_time`，与视图执行计划等价，性能无差。
4. 但 PRD（栏目 2.3/2.4）明确要求"一个账户对应一个 SQL 视图"，且用户可能用 DBeaver 直连数据库查看 → 必须真实存在。

**账户视图生成（`store/view.go`）**

```go
// CreateAccountView 幂等创建：账户创建时、启动自检时调用。
func (v *ViewManager) CreateAccountView(accountID string) error {
    if !validIdentSuffix(accountID) { return fmt.Errorf("bad id") }   // ^[a-z0-9_]+$
    name := "v_account_" + accountID
    sql := fmt.Sprintf(`CREATE VIEW IF NOT EXISTS %s AS
        SELECT * FROM v_transactions_signed WHERE account_id = '%s'`, name, accountID)
    return v.db.Exec(sql).Error
}
// DropAccountView / RebuildAccountView 同构。
// 启动时 StartReconcile()：遍历 accounts + 已出现过的 account_id，补齐缺失视图；
// 对已不存在账户的孤儿视图不删除（保留历史可查），仅记 warn 日志。
```

**账本视图生成（规则 → WHERE 子句）**

```go
// BuildLedgerWhere 把规则目标展开成 (whereSQL, args)。同 match_mode：
//   or  → 各组之间 OR（PRD 语义："命中任一绑定即纳入"）
//   all → 各组之间 AND
func (v *ViewManager) BuildLedgerWhere(ledgerID string) (string, []any, error) {
    rules := loadRules(ledgerID)                  // 只取 is_active=1
    var orParts []string; var args []any
    for _, r := range rules {
        groups := map[string][]string{}           // target_type → ids
        for _, t := range loadTargets(r.ID) { groups[t.Type] = append(groups[t.Type], t.ID) }
        var andParts []string
        if ids := groups["category_l1"]; len(ids) > 0 {
            andParts = append(andParts, "t.category_id IN ("+ph(len(ids))+")"); args = append(args, toAny(ids)...)
        }
        if ids := groups["category_l2"]; len(ids) > 0 {
            andParts = append(andParts, "t.sub_category_id IN ("+ph(len(ids))+")"); args = append(args, toAny(ids)...)
        }
        if ids := groups["account"]; len(ids) > 0 {
            andParts = append(andParts, "t.account_id IN ("+ph(len(ids))+")"); args = append(args, toAny(ids)...)
        }
        if ids := groups["merchant"]; len(ids) > 0 {
            andParts = append(andParts, "t.merchant_id IN ("+ph(len(ids))+")"); args = append(args, toAny(ids)...)
        }
        if ids := groups["tag"]; len(ids) > 0 {
            andParts = append(andParts, "EXISTS (SELECT 1 FROM transaction_tags tt "+
                "WHERE tt.transaction_id = t.id AND tt.tag_id IN ("+ph(len(ids))+"))")
            args = append(args, toAny(ids)...)
        }
        // 单个 rule 内部：不同类型之间 OR（"分类命中 或 标签命中"）
        if len(andParts) > 0 {
            joiner := " OR "
            if r.MatchMode == "all" { joiner = " AND " }
            orParts = append(orParts, "("+strings.Join(andParts, joiner)+")")
        }
    }
    if len(orParts) == 0 { return "1=0", nil, nil }   // 无规则 → 空账本
    return "(" + strings.Join(orParts, " OR ") + ")", args, nil
}
```

**视图 DDL**（每次规则变更时 `DROP VIEW IF EXISTS` + `CREATE VIEW`）：

```sql
CREATE VIEW v_ledger_ldg_a1b2c3d4 AS
SELECT t.* FROM v_transactions_signed t
WHERE /* BuildLedgerWhere 生成的文本（id 已校验为 ^[A-Za-z0-9_]+$） */;
```

**API 查询路径**（真实使用的路径）：

```go
// 账本筛选：把 BuildLedgerWhere 结果拼进交易列表 SQL 的 WHERE
if q.LedgerID != "" {
    w, args, _ := views.BuildLedgerWhere(q.LedgerID)
    where = append(where, strings.ReplaceAll(w, "t.", "transactions."))
    allArgs = append(allArgs, args...)
}
```

**ledgers 变更触发点**：`POST/PUT /api/ledgers/:id`、规则 CRUD、启动自检，均调用 `views.RebuildLedgerView(id)`；`DELETE /api/ledgers/:id` 调 `DropLedgerView`。

### 3.7 meta 表 key 清单

| key | 类型 | 说明 |
|---|---|---|
| `schema_version` | int | 当前 1 |
| `app_version` | str | 与二进制版本一致 |
| `auth_token` | str | 基础版 Token，种子时由 32 字节随机 hex 生成 |
| `freeze_days` | int | 默认 `30` |
| `last_frozen_at` | int(秒) | 已物化边界；`0` 表示从未物化。**语义：`transaction_time < last_frozen_at` 的交易已物化** |
| `last_materialize_at` | int | 上次物化执行的墙钟时间 |
| `seq:global` | int | 下一个 `created_seq`，从 1 开始 |
| `seq:202610` | int | 该月下一个 4 位序列（首笔返回 0） |
| `default_account_id` / `default_ledger_id` | str | 默认账户/账本 |
| `default_category_expense_id` / `default_category_income_id` | str | 默认分类 |
| `default_merchant_id` | str | 默认商户 |
| `default_txn_type` | str | `expense`（PRD 5.3 固定为支出） |
| `last_used_account_id` / `last_used_category_id` / `last_used_merchant_id` | str | 快速记账记忆（PRD 5.3） |
| `high_amount_threshold_cents` | int | 默认 `50000`（500 元） |
| `quick_templates` | json | 常用模板数组（PRD 5.5） |
| `ui_prefs` | json | `{theme, trendChartType, pieChartType, timePreset, dashboardLayout}` |
| `last_backup_at` | int | 每周一检测用 |
| `last_cleanup_at` | int | 截图/备份清理用 |
| `frozen_write_flag` | `0|1` | 曾发生冻结区写入 → 需要下次强制全量重物化 |

### 3.8 视图总结（数据库内可见对象）

| 对象 | 类型 | 生成时机 |
|---|---|---|
| `transactions` 等 13 张表 | TABLE | 迁移 |
| `v_transactions_signed` | VIEW | 迁移 |
| `v_account_<account_id>` | VIEW | 账户创建 + 启动自检 |
| `v_ledger_<ledger_id>` | VIEW | 账本创建/规则变更 + 启动自检 |

---

## 4. 数据模型（Go / TS）

### 4.1 Go model（`internal/store/models.go`）

```go
type Transaction struct {
    ID                  string `gorm:"column:id;primaryKey"`
    CreatedSeq          int64  `gorm:"column:created_seq;uniqueIndex"`
    TransactionTime     int64  `gorm:"column:transaction_time;index"`
    LocalDate           string `gorm:"column:local_date;index"`
    TransactionType     string `gorm:"column:transaction_type"`
    AmountCents         int64  `gorm:"column:amount_cents"`
    Currency            string `gorm:"column:currency"`
    FxRate              float64 `gorm:"column:fx_rate"`
    AccountID           string `gorm:"column:account_id;index"`
    TransferDirection   *string `gorm:"column:transfer_direction"`
    TransferGroupID     *string `gorm:"column:transfer_group_id;index"`
    CounterAccountID    *string `gorm:"column:counter_account_id"`
    CategoryID          *string `gorm:"column:category_id;index"`
    SubCategoryID       *string `gorm:"column:sub_category_id;index"`
    MerchantID          *string `gorm:"column:merchant_id;index"`
    MerchantText        string `gorm:"column:merchant_text"`
    RegionID            *string `gorm:"column:region_id"`
    CounterpartyName    string `gorm:"column:counterparty_name"`
    CounterpartyAccount string `gorm:"column:counterparty_account"`
    Remark              string `gorm:"column:remark"`
    OCRRawText          string `gorm:"column:ocr_raw_text"`
    ScreenshotID        string `gorm:"column:screenshot_id"`
    IsAdjustment        int    `gorm:"column:is_adjustment"`
    AdjustmentType      *string `gorm:"column:adjustment_type"`
    RelatedTransactionID *string `gorm:"column:related_transaction_id"`
    Source              string `gorm:"column:source"`
    ExternalID          string `gorm:"column:external_id"`
    AccountBalanceAfter *int64 `gorm:"column:account_balance_after"`
    TotalBalanceAfter   *int64 `gorm:"column:total_balance_after"`
    MaterializedAt      *int64 `gorm:"column:materialized_at"`
    DeletedAt           *int64 `gorm:"column:deleted_at;index"`
    CreatedAt           int64  `gorm:"column:created_at"`
    UpdatedAt           int64  `gorm:"column:updated_at"`
}
func (Transaction) TableName() string { return "transactions" }
```

`Account / Category / Tag / Merchant / Region / Ledger / LedgerRule / LedgerRuleTarget / TransactionItem / Screenshot / Meta` 同构定义（字段与 §3.2 DDL 一一对应，`int64` 表时间戳，`*string` 表可空）。

### 4.2 内部聚合结构

```go
type Balances struct {
    ByAccount map[string]int64 `json:"by_account"`   // acc_id → cents（不含 is_count_in_total 过滤）
    Total     int64            `json:"total"`        // 仅 is_count_in_total=1 的账户之和（含归档）
}

type TxnWriteResult struct {
    Primary   *Transaction
    Linked    []*Transaction            // 转账的另一边
    Warning   WriteWarning
}

type WriteWarning struct {
    Frozen    bool   `json:"frozen"`            // transaction_time < freeze_boundary
    Duplicate bool   `json:"duplicate"`         // 命中组合去重
    Message   string `json:"message"`
}
```

### 4.3 前端 DTO（`src/api/types.ts`，与 §5 严格一致）

```ts
export type TransactionType = 'expense' | 'income' | 'transfer'
export type AdjustmentType = 'adjust' | 'reverse'

export interface Ref { id: string; name: string }

export interface TransactionDTO {
  id: string
  created_seq: number
  transaction_time: string        // 'YYYY-MM-DD HH:mm:ss'
  local_date: string             // 'YYYY-MM-DD'
  transaction_type: TransactionType
  amount_cents: number
  signed_cents: number
  currency: string
  account: Ref
  transfer_direction?: 'out' | 'in' | null
  transfer_group_id?: string | null
  counter_account?: Ref | null
  category?: Ref | null
  sub_category?: Ref | null
  merchant?: Ref | null
  merchant_text: string
  region?: { code: string; name: string } | null
  tag_ids: string[]
  tags: { id: string; name: string; color: string }[]
  counterparty_name: string
  counterparty_account: string
  remark: string
  is_adjustment: 0 | 1
  adjustment_type?: AdjustmentType | null
  related_transaction_id?: string | null
  account_balance_after?: number | null
  total_balance_after?: number | null
  is_frozen: boolean             // t < freeze_boundary → 只读
  is_materialized: boolean       // t < last_frozen_at
  is_deleted: boolean
  deleted_at?: number | null
  source: 'manual' | 'csv' | 'ocr' | 'adjustment' | 'import'
  item_count: number
  items?: TransactionItemDTO[]
  created_at: number
  updated_at: number
}

export interface Paged<T> { total: number; page: number; page_size: number; items: T[] }

export interface ApiError {
  error: { code: string; message: string; details?: Record<string, unknown> }
}
```

---

## 5. REST API 契约

**约定**：前缀 `/api`；请求/响应 `Content-Type: application/json`（上传端点除外）；字段 `snake_case`；成功直接返回资源对象（无 envelope），失败返回统一错误体 §5.0；除 `/api/health`、`/api/auth/login` 外全部要求 `Authorization: Bearer <token>`。

### 5.0 统一错误体

```json
{ "error": { "code": "FROZEN", "message": "该交易已进入冻结区，不可直接修改，请使用调整分录",
             "details": { "transaction_time": "2026-08-01 09:00:00",
                          "freeze_boundary": "2026-09-03 12:00:00",
                          "suggest": "POST /api/transactions/{id}/adjust" } } }
```

### 5.1 meta / auth / settings

| # | Method | Path | 说明 |
|---|---|---|---|
| 1 | GET | `/api/health` | 免认证。`{status,version,db,ocr,uptime_s,now}` |
| 2 | POST | `/api/auth/login` | body `{token}` → `{ok:true,version}`；错误 401 `BAD_TOKEN` |
| 3 | GET | `/api/auth/check` | `{ok:true,expires_in:null}` |
| 4 | GET | `/api/meta` | 见下 |
| 5 | GET | `/api/settings` | 默认项 + UI 偏好 |
| 6 | PUT | `/api/settings` | 局部更新，返回完整 settings |

**GET /api/meta**

```json
{
  "version": "1.0.0",
  "schema_version": 1,
  "freeze_days": 30,
  "freeze_boundary": "2026-09-03 12:00:00",
  "last_frozen_at": "2026-09-03 02:00:00",
  "current_time": "2026-10-03 12:30:15",
  "timezone": "Asia/Shanghai",
  "default_account_id": "acc_a1b2c3d4",
  "default_category_expense_id": "cat_food0001",
  "default_category_income_id": "cat_salary01",
  "default_ledger_id": "ldg_daily001",
  "default_txn_type": "expense",
  "last_used": { "account_id": "acc_a1b2c3d4", "category_id": "cat_food0001", "merchant_id": null },
  "high_amount_threshold_cents": 50000,
  "ui_prefs": { "theme": "light", "trend_chart_type": "line", "pie_chart_type": "doughnut",
                "time_preset": "last_30d" },
  "needs_rematerialize": false,
  "ocr_available": true
}
```

**GET/PUT /api/settings** — 与上面同结构（`meta` 是读多写少的聚合视图，`settings` 是可写子集）：

```json
PUT /api/settings
{ "freeze_days": 30, "default_account_id": "acc_x", "default_category_expense_id": "cat_y",
  "default_txn_type": "expense", "high_amount_threshold_cents": 50000,
  "theme": "dark", "ui_prefs": { "trend_chart_type": "bar" },
  "quick_templates": [{ "id": "tpl_1", "name": "早餐", "amount_cents": 1000,
                        "account_id": "acc_x", "category_id": "cat_y", "merchant_id": null,
                        "sort_order": 0 }] }
```
`freeze_days` 修改后立即触发一次物化重算（`last_frozen_at` 保持不变即可，边界由 `now-freeze_days` 现算）。

### 5.2 accounts

| # | Method | Path | 说明 |
|---|---|---|---|
| 7 | GET | `/api/accounts?include_archived=false&with_balance=true` | 列表 |
| 8 | POST | `/api/accounts` | 创建（同时建视图） |
| 9 | GET | `/api/accounts/:id` | 详情（含余额、最近交易日） |
| 10 | PUT | `/api/accounts/:id` | 更新；`initial_balance_cents` 仅在冻结区无交易时允许改 |
| 11 | POST | `/api/accounts/:id/archive` | body `{archived:true}`；有交易时也允许（只能用归档代替删除） |
| 12 | DELETE | `/api/accounts/:id` | 仅当无任何交易（含已软删）时物理删除，否则 409 `ACCOUNT_IN_USE` |
| 13 | POST | `/api/accounts/reorder` | body `{items:[{id,sort_order}]}` |

```json
// GET /api/accounts?with_balance=true →
{ "items": [ {
  "id": "acc_a1b2c3d4", "name": "招商银行借记卡1", "alias": "招行",
  "display_name": "招商银行借记卡1（1234）",
  "account_type": "asset", "value_type": "current", "bank_name": "招商银行",
  "card_tail": "1234", "currency": "CNY",
  "initial_balance_cents": 100000,
  "initial_balance_time": "2026-01-01 00:00:00",
  "is_count_in_total": 1, "balance_cents": 853200,
  "sort_order": 10, "is_archived": 0, "remark": "",
  "last_transaction_time": "2026-10-03 12:30:15", "transaction_count": 42,
  "created_at": "2026-01-01 00:00:00", "updated_at": "2026-10-03 12:30:15"
} ], "total_balance_cents": 2135700 }
```

```json
// POST /api/accounts
{ "name": "招商银行借记卡1", "alias": "招行", "value_type": "current",
  "bank_name": "招商银行", "card_tail": "1234", "currency": "CNY",
  "initial_balance_cents": 100000, "initial_balance_time": "2026-01-01 00:00:00",
  "is_count_in_total": 1, "sort_order": 10, "remark": "" }
// 201 → 同上单对象；视图 v_account_acc_a1b2c3d4 已创建
```

### 5.3 categories / tags / merchants / regions / ledgers

| # | Method | Path | 说明 |
|---|---|---|---|
| 14 | GET | `/api/categories?type=expense&include_archived=false&tree=true` | `tree=true` 返回两级嵌套 |
| 15 | POST | `/api/categories` | `{name,parent_id,transaction_type,color,icon,sort_order}` |
| 16 | PUT | `/api/categories/:id` | 名称/颜色/排序/归档 |
| 17 | DELETE | `/api/categories/:id` | 被交易引用 → 409 `CATEGORY_IN_USE`；建议改归档 |
| 18 | GET | `/api/tags?include_archived=false&q=` | `{items:[{id,name,color,use_count,sort_order,is_archived}]}` |
| 19 | POST | `/api/tags` | `{name,color}`；重名 409 `DUPLICATE` |
| 20 | PUT | `/api/tags/:id` | — |
| 21 | DELETE | `/api/tags/:id` | 引用中 → 409 |
| 22 | GET | `/api/merchants?q=&type=&limit=20` | 模糊搜索（`use_count desc` 优先） |
| 23 | POST | `/api/merchants` | `{name,merchant_type,platform_code,alias,region_id}` |
| 24 | PUT | `/api/merchants/:id` | — |
| 25 | DELETE | `/api/merchants/:id` | 引用中 → 归档 |
| 26 | GET | `/api/regions/search?q=静安&limit=20` | 支持 code/汉字/拼音/首字母 |
| 27 | GET | `/api/regions/:code` | — |
| 28 | GET | `/api/ledgers?include_archived=false&with_stats=true` | 含规则与交易数 |
| 29 | POST | `/api/ledgers` | `{name,currency,remark,is_default,rules:[{match_mode,targets:[{target_type,target_id}]}]}` |
| 30 | PUT | `/api/ledgers/:id` | 更新 + 重建视图 + 重算纳入范围（视图天然一致，无需回填） |
| 31 | POST | `/api/ledgers/:id/archive` | `{archived:true}` |
| 32 | DELETE | `/api/ledgers/:id` | 删除 + `DROP VIEW` |
| 33 | GET | `/api/ledgers/:id/rules` | 规则详情 |
| 34 | PUT | `/api/ledgers/:id/rules` | 全量替换规则 + 重建视图 |

```json
// GET /api/categories?tree=true →
{ "items": [
  { "id": "cat_food0001", "name": "餐饮", "parent_id": null, "level": 1,
    "transaction_type": "expense", "color": "#F2994A", "icon": "food",
    "sort_order": 10, "is_system": 1, "is_archived": 0,
    "children": [
      { "id": "cat_lunch001", "name": "午餐", "parent_id": "cat_food0001", "level": 2,
        "transaction_type": "expense", "sort_order": 10, "is_archived": 0, "children": [] }
    ] }
] }
```

```json
// GET /api/ledgers?with_stats=true →
{ "items": [ { "id": "ldg_daily001", "name": "日常", "currency": "CNY", "remark": "",
  "is_default": 1, "sort_order": 0, "is_archived": 0, "transaction_count": 128,
  "rules": [ { "id": "rule_1a2b3c4d", "match_mode": "or",
    "targets": [ {"target_type":"category_l1","target_id":"cat_food0001"},
                 {"target_type":"tag","target_id":"tag_needed01"} ] } ],
  "created_at": "2026-01-01 00:00:00", "updated_at": "2026-01-01 00:00:00" } ] }
```

### 5.4 transactions（核心）

**35. GET /api/transactions**

Query 参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| `page` / `page_size` | int | 默认 `1` / `50`，`page_size` 上限 500 |
| `start_time` / `end_time` | string | `YYYY-MM-DD HH:mm:ss` 或 `YYYY-MM-DD`（含端点，后端补足 00:00:00 / 23:59:59） |
| `account_id` | string，可重复/逗号分隔 | 多选 |
| `category_id` | string | 一级分类（自动含其子分类，`include_sub=false` 可关闭） |
| `include_sub` | bool，默认 true | — |
| `sub_category_id` | string | 二级分类 |
| `tag_ids` | csv | 命中任一 |
| `tag_match` | `any` \| `all` | 默认 `any` |
| `merchant_id` | string | — |
| `region_id` | string | — |
| `ledger_id` | string | 账本过滤（走 `BuildLedgerWhere`） |
| `type` | csv：`expense,income,transfer` | — |
| `amount_min_cents` / `amount_max_cents` | int | — |
| `include_adjustments` | bool，默认 true | false → `is_adjustment = 0` |
| `only_adjustments` | bool，默认 false | — |
| `include_deleted` | bool，默认 false | 回收站 |
| `keyword` | string | 模糊匹配 `remark` / `merchant_text` / `counterparty_name` |
| `sort` | `time_desc`(默认) `time_asc` `amount_desc` `amount_asc` `created_desc` | — |

```json
// 200
{ "total": 132, "page": 1, "page_size": 50,
  "summary": { "income_cents": 1200000, "expense_cents": 830050, "net_cents": 369950,
               "adjustment_net_cents": -9000, "count": 132 },
  "items": [ /* TransactionDTO，见 §4.3 */ ] }
```
`summary` 为**当前过滤条件下全量**（不受分页影响）聚合，避免前端二次请求。

**36. POST /api/transactions**

```json
{
  "transaction_time": "2026-10-03 12:30:15",
  "transaction_type": "expense",
  "amount_cents": 3500,
  "account_id": "acc_a1b2c3d4",
  "category_id": "cat_food0001",
  "sub_category_id": "cat_lunch001",
  "merchant_id": "mch_mcd00001",
  "merchant_text": "",
  "region_id": "310106",
  "counterparty_name": "",
  "counterparty_account": "",
  "tag_ids": ["tag_needed01"],
  "remark": "午餐",
  "currency": "CNY",
  "source": "manual",
  "external_id": "",
  "items": [
    { "name": "巨无霸套餐", "sub_category_id": "cat_lunch001",
      "original_amount_cents": 4000, "paid_amount_cents": 3500,
      "discount_reason": "红包减免", "sort_order": 0 }
  ],
  "transfer": null,
  "allow_frozen": false,
  "allow_duplicate": false,
  "client_request_id": "uuid-optional"
}
```

规则：
- `transaction_type=transfer` 时 `transfer` 必填：`{"from_account_id":"acc_a","to_account_id":"acc_b"}`，后端生成**两行**（`transfer_group_id` 相同），`items` 必须为空。
- `transaction_time` 省略 → now。
- `allow_frozen=false` 且 `transaction_time < freeze_boundary` → `409 FROZEN`；`allow_frozen=true` 则写库并致 `meta.frozen_write_flag=1`（下次查询/启动触发全量重物化）。
- 命中组合去重且 `allow_duplicate=false` → `409 DUPLICATE`，`details.matched_transaction_id`。
- `items` 的 `paid_amount_cents` 之和允许与 `amount_cents` 不等（PRD 1.15 基础版不分摊）。

```json
// 201
{ "transaction": { /* TransactionDTO */ },
  "linked": [ /* 转账另一行；非转账为空数组 */ ],
  "balances": { "account_balance_cents": 849700, "total_balance_cents": 2132200 },
  "warning": { "frozen": false, "duplicate": false, "message": "" } }
```

**37. GET /api/transactions/:id** → `{ "transaction": {...}, "linked": [...], "adjustments": [...], "screenshot": {...}|null }`
（`adjustments` = 所有 `related_transaction_id = :id` 的调整项）

**38. PUT /api/transactions/:id** — 仅动态区

```json
{ "transaction_time": "2026-10-03 13:00:00", "amount_cents": 4000,
  "account_id": "acc_x", "category_id": "cat_y", "sub_category_id": null,
  "merchant_id": null, "merchant_text": "某某便利店", "region_id": null,
  "tag_ids": ["tag_a"], "remark": "改后", "counterparty_name": "",
  "counterparty_account": "", "items": [] }
```
- 冻结区 → `409 FROZEN`。
- 内部转账行不允许改 `amount_cents` / `account_id`（只允许改 `remark`/`tag_ids`/`transaction_time`），否则 `400 TRANSFER_IMMUTABLE`。
- 修改后触发动态区余额重算（无需异步任务，查询时现算）。

**39. DELETE /api/transactions/:id** — 软删除（仅动态区；冻结区 `409 FROZEN`）；转账行需 `?with_transfer=true`（默认 true，成对软删）。
**40. POST /api/transactions/:id/restore** — 撤销软删。
**41. DELETE /api/transactions/:id/purge** — 物理删除，仅允许 `deleted_at < now-180d`；否则 `409 RETENTION`。

**42. POST /api/transactions/:id/adjust**

```json
{ "mode": "adjust",
  "new_amount_cents": 1000,
  "new_category_id": "cat_food0001",
  "new_sub_category_id": "cat_coffee01",
  "new_merchant_text": "",
  "remark": "冲减 2026-08-01 咖啡多记 90 元" }
```

| mode | 生成交易 | 说明 |
|---|---|---|
| `adjust` | 1 笔 | 差额 `delta = new_amount - old_amount`（带原交易方向符号）；`remark` 必填；时间强制 `now` |
| `reverse` | 2 笔 | ①全额冲销：方向相反、金额 = 原金额；②正确重录：按新字段同向、金额 = `new_amount` |

```json
// 200
{ "created": [ { /* TransactionDTO */ } ],
  "delta_cents": -9000,
  "balances": { "account_balance_cents": 858700, "total_balance_cents": 2141200 },
  "note": "原交易 2026-08-01 09:00:00 已在冻结区，报表历史区间仍显示原金额" }
```

**43. POST /api/transactions/batch-delete** — `{ids:[...]}` 便于批量清理（动态区，部分失败返回 `{succeeded:[], failed:[{id,code}]}`）。

### 5.5 stats

通用查询参数（全部端点都支持）：`start_time`、`end_time`、`account_id[]`、`category_id`、`tag_ids`、`merchant_id`、`type`、`ledger_id`、`exclude_adjustments`（默认 true）。

| # | Method | Path |
|---|---|---|
| 44 | GET | `/api/stats/summary` |
| 45 | GET | `/api/stats/trend?granularity=day\|week\|month\|year` |
| 46 | GET | `/api/stats/category-breakdown?type=expense&level=1\|2` |
| 47 | GET | `/api/stats/account-balance-series?account_ids=a,b&granularity=day` |
| 48 | GET | `/api/stats/category-ranking?type=expense&limit=10` |
| 49 | GET | `/api/stats/high-amount?threshold_cents=&limit=20` |

```json
// 44 →
{ "start_time": "2026-09-04 00:00:00", "end_time": "2026-10-03 23:59:59",
  "income_cents": 1200000, "expense_cents": 830050, "net_cents": 369950,
  "total_balance_cents": 2135700,
  "adjustment_income_cents": 9000, "adjustment_expense_cents": 0,
  "adjustment_net_cents": 9000,
  "transaction_count": 132, "day_count": 30, "avg_expense_per_day_cents": 27668 }

// 45 →
{ "granularity": "day",
  "points": [ { "bucket": "2026-09-04", "label": "09-04",
                "income_cents": 0, "expense_cents": 12500, "net_cents": -12500,
                "adjustment_cents": 0, "count": 3 } ] }

// 46 →
{ "type": "expense", "level": 1, "total_cents": 830050,
  "items": [ { "category_id": "cat_food0001", "name": "餐饮", "parent_id": null,
               "amount_cents": 320000, "percent": 38.55, "transaction_count": 42,
               "children": [ { "category_id": "cat_lunch001", "name": "午餐",
                               "amount_cents": 120000, "percent": 14.45 } ] } ] }

// 47 →
{ "granularity": "day",
  "series": [ { "account_id": "acc_a1b2c3d4", "name": "招商银行借记卡1（1234）",
                "points": [ { "bucket": "2026-09-04", "balance_cents": 800000 } ] } ],
  "total_points": [ { "bucket": "2026-09-04", "balance_cents": 2135700 } ] }

// 48 →
{ "items": [ { "rank": 1, "category_id": "cat_food0001", "name": "餐饮",
               "amount_cents": 320000, "percent": 38.55, "transaction_count": 42 } ] }

// 49 →
{ "threshold_cents": 50000, "auto_threshold": true,
  "items": [ /* TransactionDTO */ ], "total_cents": 320000 }
```

### 5.6 reports

| # | Method | Path | 关键参数 |
|---|---|---|---|
| 50 | GET | `/api/reports/category` | `level=1\|2&type=&start_time&end_time&compare=prev_period\|prev_year\|none` |
| 51 | GET | `/api/reports/account` | `start_time&end_time` |
| 52 | GET | `/api/reports/tag` | `tag_ids&start_time&end_time` |
| 53 | GET | `/api/reports/period-compare` | `period=month\|quarter\|year&base=2026-10&exclude_adjustments=true` |

```json
// 50 →
{ "level": 1, "total_cents": 830050, "exclude_adjustments": true,
  "items": [ { "category_id": "cat_food0001", "name": "餐饮", "amount_cents": 320000,
               "percent": 38.55, "prev_period_cents": 300000, "prev_period_pct": 6.67,
               "prev_year_cents": 280000, "prev_year_pct": 14.29,
               "transaction_count": 42 } ] }

// 51 →
{ "items": [ { "account_id": "acc_a1b2c3d4", "name": "招商银行借记卡1（1234）",
               "income_cents": 1200000, "expense_cents": 830050,
               "transfer_in_cents": 50000, "transfer_out_cents": 20000,
               "net_cents": 369950, "balance_cents": 853200 } ],
  "total_balance_cents": 2135700 }

// 52 →
{ "items": [ { "tag_id": "tag_needed01", "name": "必要", "color": "#27AE60",
               "amount_cents": 210000, "percent": 25.30, "transaction_count": 18 } ] }

// 53 →
{ "period": "month", "current": { "label": "2026-10", "start_time": "2026-10-01 00:00:00",
    "end_time": "2026-10-31 23:59:59", "income_cents": 120000, "expense_cents": 83000,
    "net_cents": 37000 },
  "previous": { "label": "2026-09", "start_time": "...", "end_time": "...",
    "income_cents": 100000, "expense_cents": 90000, "net_cents": 10000 },
  "prev_year": { "label": "2025-10", "income_cents": 95000, "expense_cents": 80000,
    "net_cents": 15000 },
  "diff": { "income_cents": 20000, "income_pct": 20.0,
            "expense_cents": -7000, "expense_pct": -7.78 },
  "exclude_adjustments": true }
```

### 5.7 import

**54. POST /api/import/csv/preview** — `multipart/form-data`，字段 `file`（必填）、`account_id`（可选，作为默认值）、`source`（`auto|wechat|alipay`，默认 `auto`）。

```json
// 200
{ "batch_id": "imp_9f3a1c2b", "source": "wechat", "encoding": "utf-8",
  "columns": ["交易时间","交易类型","交易对方","商品","收/支","金额(元)","支付方式","当前状态","交易单号"],
  "mapping": { "transaction_time": "交易时间", "transaction_type": "收/支",
               "amount": "金额(元)", "counterparty": "交易对方", "remark": "商品",
               "external_id": "交易单号", "status": "当前状态" },
  "summary": { "total_rows": 128, "parsed_ok": 121, "skipped": 7,
               "dup_count": 5, "frozen_count": 30 },
  "rows": [ { "row_no": 3, "raw": { "交易时间": "2026-08-01 09:00:00", "收/支": "支出",
                                    "金额(元)": "¥100.00", "交易对方": "星巴克" },
      "parsed": { "transaction_time": "2026-08-01 09:00:00", "transaction_type": "expense",
                  "amount_cents": 10000, "merchant_text": "星巴克", "remark": "咖啡",
                  "external_id": "10001234567890", "currency": "CNY" },
      "dup": { "is_dup": true, "reason": "external_id", "matched_transaction_id": "20260801090001000X" },
      "frozen": true, "warnings": ["交易时间落在冻结区，确认后不可直接修改"],
      "need_confirm": ["category_id"] } ],
  "suggest_defaults": { "account_id": "acc_a1b2c3d4", "category_id": "cat_food0001" } }
```

解析结果同时落盘 `data/tmp/import/imp_9f3a1c2b.json`（24h 后清理），commit 时按 `batch_id` 读取。

**55. POST /api/import/csv/commit**

```json
{ "batch_id": "imp_9f3a1c2b",
  "row_nos": [3, 4, 5],
  "defaults": { "account_id": "acc_a1b2c3d4", "category_id": "cat_food0001",
                "sub_category_id": null, "tag_ids": [], "merchant_type": "online" },
  "overrides": [ { "row_no": 4, "transaction_type": "income", "amount_cents": 2000,
                   "category_id": "cat_other001", "remark": "改后" } ],
  "skip_duplicates": true,
  "allow_frozen": true }
```
```json
// 200
{ "created": 118, "skipped_dup": 5, "skipped_invalid": 5,
  "frozen_created": 30, "rematerialize_triggered": true,
  "transaction_ids": ["...", "..."],
  "batch_id": "imp_9f3a1c2b" }
```

### 5.8 ocr

| # | Method | Path | 说明 |
|---|---|---|---|
| 56 | POST | `/api/ocr/recognize` | `multipart/form-data`：`file`（图片）或 `screenshot_id`；`model=auto\|light\|heavy`；`mode=sync\|async` |
| 57 | POST | `/api/ocr/recognize-batch` | `multipart` 多文件，立即返回 `{task_ids:[]}` |
| 58 | GET | `/api/ocr/tasks/:id` | 任务状态与结果 |
| 59 | GET | `/api/ocr/model` | 代理 `GET /api/v1/model` |
| 60 | PUT | `/api/ocr/model` | `{model:"ollama:qwen2.5vl:7b"}` 热切换 |

```json
// 56 (mode=async) 202
{ "task_id": "ocr_7c1e2a3f", "screenshot_id": "202610031230150000X",
  "status": "processing", "original_path": "screenshots/originals/2026-10-03/202610031230150000X.jpg",
  "compressed_path": "screenshots/compressed/2026-10-03/202610031230150000X.jpg" }

// 58 (done)
{ "task_id": "ocr_7c1e2a3f", "status": "done",
  "elapsed_ms": 3420, "model_used": "ollama:qwen2.5vl:7b",
  "result": { "id": "202610031230150000X", "source": "pc_local",
    "transaction_time": "2026-10-03 12:30:15", "transaction_type": "expense",
    "amount_cents": 3500, "account": "微信零钱", "account_id": null,
    "category": "餐饮", "category_id": "cat_food0001",
    "sub_category": null, "merchant": "麦当劳静安寺店", "merchant_id": null,
    "region": null, "tag_ids": [], "remark": "午餐",
    "confidence": { "amount": 0.98, "transaction_time": 0.95, "merchant": 0.92 },
    "need_review": true, "warnings": ["account 未在账户表中找到"] },
  "ocr_result_path": "ocr-results/2026-10-03/202610031230150000X.json" }

// 56 (OCR 服务不可用) 503
{ "error": { "code": "OCR_UNAVAILABLE", "message": "OCR 服务未启动，可手动录入或稍后重试",
             "details": { "endpoint": "http://127.0.0.1:8771" } } }
```

> **注意**：`result` 里 `confidence` / `raw_text` / `model_used` 属于 PRD 7.3 的"不返回前端"字段。这里 `model_used` 与 `elapsed_ms` 放在**顶层**用于运维展示，`confidence` 与 `raw_text` 不返回（`raw_text` 仅落盘 JSON 与 `transactions.ocr_raw_text`）。

### 5.9 backup

| # | Method | Path | 说明 |
|---|---|---|---|
| 61 | POST | `/api/backup/create` | `wal_checkpoint(TRUNCATE)` → zip → 返回文件信息 |
| 62 | GET | `/api/backup/list` | `{items:[{file,size_bytes,created_at,checksum}]}` |
| 63 | POST | `/api/backup/restore` | `{file:"ledger-20261003.zip", confirm:true}` → 校验 zip → 关闭连接 → 替换 → 重开 → 全量校验 |
| 64 | DELETE | `/api/backup/:file` | 删除指定备份 |

```json
// 61 → 201
{ "file": "ledger-20261003-1200.zip",
  "path": "data/backups/2026-10-03/ledger-20261003-1200.zip",
  "size_bytes": 2457600, "checksum": "sha256:ab12...", "created_at": "2026-10-03 12:00:00" }
```
恢复后返回 `{ "restored": true, "from": "ledger-20261003-1200.zip", "verify": {"ok":true,"checked_rows":12843} }`；恢复期间 API 返回 `503 RESTORING`。

### 5.10 截图与清理

| # | Method | Path | 说明 |
|---|---|---|---|
| 65 | GET | `/api/screenshots/:id` | 元数据 + 可访问 URL |
| 66 | GET | `/api/screenshots/:id/file?size=compressed\|original` | 返回二进制（`image/jpeg`） |
| 67 | POST | `/api/maintenance/cleanup` | 手动触发清理（截图 30/150 天、备份 150 天、import tmp 24h） |
| 68 | POST | `/api/maintenance/rematerialize` | `{full:true}` 强制全量重物化，返回 `{rows: n, seconds: 1.2}` |
| 69 | GET | `/api/maintenance/verify` | 全量余额校验，返回 `{ok, checked_rows, mismatches:[]}` |

**端点总数 69 个。**

---

## 6. 核心算法（工程师必须逐行照做）

### 6.1 19 位业务 ID

**结构**：`yyyyMMddHHmmss`(14) + `NNNN`(4，当月序列，首笔 `0000`) + `C`(1，校验码) = 19

**校验码**：前 18 位数字逐位求和 `s`，`r = s % 11`；`r == 10` → `'X'`，否则 → `strconv.Itoa(r)`。

```go
// internal/pkg/ids/ids.go
func CheckDigit(body18 string) byte {          // body18 必须为 18 个 ASCII 数字
    sum := 0
    for i := 0; i < 18; i++ { sum += int(body18[i] - '0') }
    if r := sum % 11; r == 10 { return 'X' } else { return byte('0' + r) }
}

func Verify(id string) bool {
    if len(id) != 19 { return false }
    for i := 0; i < 18; i++ { if id[i] < '0' || id[i] > '9' { return false } }
    c := id[18]
    okNum := c >= '0' && c <= '9'
    if !okNum && c != 'X' { return false }
    return CheckDigit(id[:18]) == c
}

// 短 ID：'acc_'/'cat_'/'tag_'/'mch_'/'ldg_'/'rule_' + 8 位小写 base32
func ShortID(prefix string) string { /* crypto/rand 5 字节 → base32hex 小写 8 位 */ }
```

**月度序列原子递增**（必须在 `BEGIN IMMEDIATE` 写事务内）：

```go
// AllocSeq 返回本次可用的序列号：首次为 0，之后 1,2,3...
// meta['seq:YYYYMM'] 语义 = "下一个待分配值"
func AllocMonthSeq(tx *sql.Tx, ym string) (int, error) {
    key := "seq:" + ym
    var cur int
    err := tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?`, key).Scan(&cur)
    if err == sql.ErrNoRows {
        cur = 0
        tx.Exec(`INSERT INTO meta(key,value,updated_at) VALUES(?,?,?)`, key, "1", nowUnix())
        return 0, nil
    }
    if _, err = tx.Exec(`UPDATE meta SET value=?, updated_at=? WHERE key=?`,
        strconv.Itoa(cur+1), nowUnix(), key); err != nil { return 0, err }
    return cur, nil
}

// 全流程：生成 ID（含冲突重试）
func NewTransactionID(tx *sql.Tx, t time.Time) (string, error) {
    ts := timeutil.Local(t).Format("20060102150405")
    for attempt := 0; attempt < 8; attempt++ {
        seq, err := AllocMonthSeq(tx, ts[:6])          // 'YYYYMM'
        if err != nil { return "", err }
        body := ts + fmt.Sprintf("%04d", seq%10000)    // seq 溢出 4 位时回绕
        id := body + string(CheckDigit(body))
        var n int
        tx.QueryRow(`SELECT COUNT(1) FROM transactions WHERE id=?`, id).Scan(&n)
        if n == 0 { return id, nil }                    // 未冲突
        // 冲突：继续下一次分配（同秒 + 回绕的极少数场景）
    }
    return "", apierr.New("SEQUENCE_OVERFLOW", "当月交易序列已耗尽，请检查数据")
}
```

**`created_seq` 分配**（同事务内）：

```go
func AllocGlobalSeq(tx *sql.Tx) (int64, error) {
    var cur int64
    err := tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key='seq:global'`).Scan(&cur)
    if err == sql.ErrNoRows {
        cur = 1
        tx.Exec(`INSERT INTO meta(key,value,updated_at) VALUES('seq:global','2',?)`, nowUnix())
        return 1, nil
    }
    tx.Exec(`UPDATE meta SET value=?, updated_at=? WHERE key='seq:global'`, cur+1, nowUnix())
    return cur, nil
}
```
> `created_seq` 单调递增且**永不复用**（purge 不回收），保证 `UNIQUE` 恒成立。

**事务写法**（所有多语句写操作）：

```go
func (s *Store) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
    conn, _ := s.sqlDB.Conn(ctx)             // MaxOpenConns=1，可安全串行
    defer conn.Close()
    tx, err := conn.BeginTx(ctx, &sql.TxOptions{})   // 等价 BEGIN IMMEDIATE（单连接已无并发写）
    if err != nil { return err }
    if err = fn(tx); err != nil { tx.Rollback(); return err }
    return tx.Commit()
}
```

### 6.2 排序键

**统一排序键** `ORDER BY transaction_time ASC, created_seq ASC`（余额正序累计）与 `transaction_time DESC, created_seq DESC`（列表倒序）。任何 SQL 不得只按 `transaction_time` 排序。

### 6.3 两个"边界"的严格定义

| 名称 | 定义 | 用途 |
|---|---|---|
| `freeze_boundary` | `now - freeze_days*86400`（秒） | **写保护**：`transaction_time < freeze_boundary` 的交易禁止改/删 |
| `last_frozen_at` | 已物化的上界，存 `meta` | **物化覆盖**：`transaction_time < last_frozen_at` 的交易已有 `account_balance_after` |

**不变量**：`last_frozen_at <= freeze_boundary + 86400`（每天跑一次，最多滞后一天）。区间 `[last_frozen_at, freeze_boundary)` 内的交易：**已只读、未物化**，余额查询时必须动态计算（正确性由 §6.5 保证）。

### 6.4 物化（`Materialize`）

```go
type MatResult struct {
    NewBoundary   int64
    RowsProcessed int
    ElapsedMS     int64
    VerifyOK      bool
}

func (m *Materializer) Materialize(ctx context.Context, now time.Time) (MatResult, error) {
    freezeDays := m.meta.Int("freeze_days", 30)
    newBoundary := now.Unix() - int64(freezeDays)*86400

    return res, m.store.WriteTx(ctx, func(tx *sql.Tx) error {
        last := m.meta.IntTx(tx, "last_frozen_at", 0)
        if last >= newBoundary && last != 0 { return nil }   // 无需推进（幂等）

        // ---- 1. 取起点余额快照 ----
        // 结构： accountID → (balance, countInTotal)
        balances := map[string]int64{}
        countIn := map[string]bool{}
        if last == 0 {
            rows := tx.Query(`SELECT id, initial_balance_cents, is_count_in_total FROM accounts`)
            for rows.Next() { a, b, c := scan(); balances[a] = b; countIn[a] = c == 1 }
        } else {
            // 每账户在 last 之前最后一笔的物化余额；若无则 initial_balance_cents
            rows := tx.Query(`
              SELECT a.id, a.is_count_in_total,
                COALESCE((SELECT t.account_balance_after FROM transactions t
                           WHERE t.account_id = a.id AND t.deleted_at IS NULL
                             AND t.transaction_time < ? AND t.account_balance_after IS NOT NULL
                           ORDER BY t.transaction_time DESC, t.created_seq DESC LIMIT 1),
                         a.initial_balance_cents) AS bal
              FROM accounts a`, last)
            for rows.Next() { a, c, b := scan(); balances[a] = b; countIn[a] = c == 1 }
        }

        // ---- 2. 按排序键逐笔累计 ----
        rows := tx.Query(`
          SELECT id, account_id, transaction_type, transfer_direction, amount_cents
          FROM transactions
          WHERE deleted_at IS NULL AND transaction_time >= ? AND transaction_time < ?
          ORDER BY transaction_time ASC, created_seq ASC`, last, newBoundary)
        type upd struct{ id string; acc, tot int64 }
        var batch []upd
        for rows.Next() {
            id, acc, typ, dir, amt := scan()
            delta := 0 int64
            switch {
            case typ == "expense":  delta = -amt
            case typ == "income":   delta =  amt
            case dir == "out":      delta = -amt
            case dir == "in":       delta =  amt
            }
            balances[acc] += delta
            total := int64(0)
            for a, b := range balances { if countIn[a] { total += b } }   // 归档账户仍计入
            batch = append(batch, upd{id, balances[acc], total})
        }
        // ---- 3. 回写（复用 prepared statement，每 500 条 flush）----
        stmt := tx.Prepare(`UPDATE transactions SET account_balance_after=?, total_balance_after=?,
                            materialized_at=? WHERE id=?`)
        for _, u := range batch { stmt.Exec(u.acc, u.tot, nowUnix(), u.id) }
        stmt.Close()

        // ---- 4. 推进边界 + 清标志 ----
        m.meta.SetTx(tx, "last_frozen_at", newBoundary)
        m.meta.SetTx(tx, "last_materialize_at", now.Unix())
        m.meta.SetTx(tx, "frozen_write_flag", 0)

        // ---- 5. 衔接校验（失败则回滚，阻止后续写入）----
        if ok, err := verifySeam(tx, newBoundary); err != nil || !ok {
            return apierr.New("MATERIALIZE_FAILED", "物化衔接校验失败，已回滚")
        }
        return nil
    })
}
```

**`verifySeam(tx, boundary)`**（衔接点校验）：

```sql
-- 期望值：完全独立地按公式重算
SELECT a.id,
       a.initial_balance_cents + COALESCE(SUM(CASE
         WHEN t.transaction_type='expense' THEN -t.amount_cents
         WHEN t.transaction_type='income'  THEN  t.amount_cents
         WHEN t.transfer_direction='out'   THEN -t.amount_cents
         ELSE t.amount_cents END), 0) AS expected
FROM accounts a
LEFT JOIN transactions t ON t.account_id = a.id
     AND t.deleted_at IS NULL AND t.transaction_time < :boundary
GROUP BY a.id;
```
与"物化后该账户最后一条 `account_balance_after`"逐账户比对，任一不一致 → 返回 error 并回滚整个事务。

**触发时机**

| 时机 | 调用 |
|---|---|
| 进程启动 | `main.go` goroutine 里 `Materialize(now)`，失败只记 error 不阻塞启动 |
| 每日 03:00 | 后台 ticker（`time.NewTicker(time.Hour)`，判断跨日且未跑过） |
| 查询前补偿 | `GET /api/transactions`、`/stats/*`、`/accounts?with_balance=true` 前检查 `last_frozen_at` 落后超过 1 天 → 同步补跑一次（有 30s 节流锁） |
| 冻结区写入后 | `meta.frozen_write_flag=1` → 下次物化走 `ForceFull` |
| 手动 | `POST /api/maintenance/rematerialize` |

**`ForceFull`（冻结区被写入后的修正）**

```go
func (m *Materializer) ForceFull(ctx, now) error {
    // 把 last_frozen_at 重置为 0，然后走 Materialize 的 last==0 分支（从 initial_balance 全量重算）
    return m.store.WriteTx(ctx, func(tx *sql.Tx) error {
        m.meta.SetTx(tx, "last_frozen_at", 0)
        tx.Exec(`UPDATE transactions SET account_balance_after=NULL, total_balance_after=NULL`)
        return nil
    }) // 紧接着调用 Materialize(now)
}
```
**复杂度**：10 万笔 ≈ 2–3 秒（单事务、prepared statement）。冻结区写入是极少数路径（CSV 强导历史），可接受。

### 6.5 当前余额（动态区计算）

**一次查询拿全部账户余额 + 总余额**（`service/balance.go`）：

```sql
-- :last = meta.last_frozen_at（0 时第一段自动落空，回到 initial）
SELECT a.id, a.is_count_in_total,
  COALESCE((
    SELECT t.account_balance_after FROM transactions t
     WHERE t.account_id = a.id AND t.deleted_at IS NULL
       AND t.transaction_time < :last AND t.account_balance_after IS NOT NULL
     ORDER BY t.transaction_time DESC, t.created_seq DESC LIMIT 1
  ), a.initial_balance_cents)
  + COALESCE((
    SELECT SUM(CASE
      WHEN x.transaction_type = 'expense' THEN -x.amount_cents
      WHEN x.transaction_type = 'income'  THEN  x.amount_cents
      WHEN x.transfer_direction = 'out'   THEN -x.amount_cents
      WHEN x.transfer_direction = 'in'    THEN  x.amount_cents
      ELSE 0 END)
      FROM transactions x
     WHERE x.account_id = a.id AND x.deleted_at IS NULL AND x.transaction_time >= :last
  ), 0) AS balance_cents
FROM accounts a
ORDER BY a.sort_order, a.created_at;
```
```go
// 组装
b := Balances{ByAccount: map[string]int64{}}
for _, r := range rows { b.ByAccount[r.ID] = r.BalanceCents; if r.CountInTotal == 1 { b.Total += r.BalanceCents } }
```
**校验关系**（PRD 2.11）：任意账户 `balance = initial + Σfrozen_signed + Σdyn_signed`；`Σ` 覆盖 `is_count_in_total=1` 的账户（**含归档**）。

**列表/详情里单笔的 `account_balance_after`**（动态区用窗口函数）：

```sql
-- 账户维度动态区逐笔余额
WITH base AS (
  SELECT a.id AS account_id,
    COALESCE((SELECT t.account_balance_after FROM transactions t
              WHERE t.account_id=a.id AND t.deleted_at IS NULL
                AND t.transaction_time < :last AND t.account_balance_after IS NOT NULL
              ORDER BY t.transaction_time DESC, t.created_seq DESC LIMIT 1),
             a.initial_balance_cents) AS base_cents
  FROM accounts a
), dyn AS (
  SELECT x.id, x.account_id, x.transaction_time, x.created_seq,
    CASE WHEN x.transaction_type='expense' THEN -x.amount_cents
         WHEN x.transaction_type='income'  THEN  x.amount_cents
         WHEN x.transfer_direction='out'   THEN -x.amount_cents
         ELSE x.amount_cents END AS delta_cents
  FROM transactions x
  WHERE x.deleted_at IS NULL AND x.transaction_time >= :last
)
SELECT d.id,
       b.base_cents + SUM(d.delta_cents) OVER (
         PARTITION BY d.account_id ORDER BY d.transaction_time, d.created_seq
         ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS account_balance_after
FROM dyn d JOIN base b ON b.account_id = d.account_id;
```

```sql
-- 全局动态区逐笔总余额（只累计 is_count_in_total=1 的账户）
WITH base AS (
  SELECT COALESCE(SUM(
    COALESCE((SELECT t.account_balance_after FROM transactions t
              WHERE t.account_id=a.id AND t.deleted_at IS NULL
                AND t.transaction_time < :last AND t.account_balance_after IS NOT NULL
              ORDER BY t.transaction_time DESC, t.created_seq DESC LIMIT 1),
             a.initial_balance_cents)), 0) AS base_total
  FROM accounts a WHERE a.is_count_in_total = 1
), dyn AS (
  SELECT x.id, x.transaction_time, x.created_seq,
    CASE WHEN a.is_count_in_total = 0 THEN 0
         WHEN x.transaction_type='expense' THEN -x.amount_cents
         WHEN x.transaction_type='income'  THEN  x.amount_cents
         WHEN x.transfer_direction='out'   THEN -x.amount_cents
         ELSE x.amount_cents END AS delta_cents
  FROM transactions x JOIN accounts a ON a.id = x.account_id
  WHERE x.deleted_at IS NULL AND x.transaction_time >= :last
)
SELECT d.id, (SELECT base_total FROM base) + SUM(d.delta_cents) OVER (
         ORDER BY d.transaction_time, d.created_seq
         ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS total_balance_after
FROM dyn d;
```
**DTO 填充规则**：`t < last_frozen_at` → 直接读物化字段；`t >= last_frozen_at` → 用上面两张表的映射；`account_balance_after` 允许 `null`（理论上不会）。

### 6.6 内部转账

**写入**（同一事务，同一个 `transfer_group_id`）：

```go
func (s *TxnService) createTransfer(in CreateTransferInput) (*TxnWriteResult, error) {
    return res, s.store.WriteTx(ctx, func(tx *sql.Tx) error {
        gid := ids.ShortID("tg_")
        now := timeutil.NowLocal()
        // 转出行
        outID, _ := ids.NewTransactionID(tx, now)
        seq, _   := ids.AllocGlobalSeq(tx)
        insert(tx, Transaction{
            ID: outID, CreatedSeq: seq, TransactionTime: in.Time, LocalDate: in.LocalDate,
            TransactionType: "transfer", AmountCents: in.AmountCents,
            AccountID: in.FromAccountID, TransferDirection: "out",
            TransferGroupID: gid, CounterAccountID: in.ToAccountID,
            CategoryID: nil, Remark: in.Remark, Source: "manual",
        })
        // 转入行（同金额、反方向、独立 ID 与 created_seq）
        inID, _ := ids.NewTransactionID(tx, now)
        seq2, _ := ids.AllocGlobalSeq(tx)
        insert(tx, Transaction{ /* AccountID: ToAccountID, TransferDirection: "in", 其余同上 */ })
        return nil
    })
}
```
- **不计入收支统计**：`v_transactions_signed.stat_expense_cents / stat_income_cents` 对 `transfer` 恒为 0。
- **`signed_cents`**：`out → -amount`，`in → +amount`，净和为 0。
- 双边金额必须相等（`CHECK` 无法跨行，由 service 层强制）。

### 6.7 调整分录 / 反冲重录

前置：`old := Get(id)`；校验 `old.is_adjustment == 0`（调整项不能再被调整，避免链）；`remark` 必填；`adjust_time = now`（PRD 2.11 "最好强制为当前时间"）；若 `delta == 0 && mode == "adjust"` → `400 NO_EFFECT`。

```go
// mode = adjust：只补差额一笔
func (s *AdjustService) Adjust(old *Transaction, in AdjustInput) ([]*Transaction, int64, error) {
    if old.TransactionType == "transfer" { return s.adjustTransfer(old, in) }

    delta := in.NewAmountCents - old.AmountCents        // 差额（正=原记少了）
    if delta == 0 { return nil, 0, apierr.New("NO_EFFECT", "金额未变化") }
    var typ string
    switch {
    case old.TransactionType == "expense":
        if delta > 0 { typ = "expense" } else { typ = "income" }   // 少记支出→补支出；多记支出→冲收入
    case old.TransactionType == "income":
        if delta > 0 { typ = "income" } else { typ = "expense" }
    }
    // 注意：金额一律存正数；方向由 typ 决定
    t := &Transaction{
        TransactionType: typ, AmountCents: abs(delta),
        AccountID: old.AccountID, TransactionTime: nowUnix(), LocalDate: nowLocalDate(),
        CategoryID: pick(in.NewCategoryID, old.CategoryID),
        SubCategoryID: pickPtr(in.NewSubCategoryID, old.SubCategoryID),
        MerchantID: old.MerchantID, MerchantText: old.MerchantText,
        Remark: in.Remark, Source: "adjustment",
        IsAdjustment: 1, AdjustmentType: ptr("adjust"), RelatedTransactionID: &old.ID,
    }
    /* 分配 ID + created_seq，插入，返回 [t], 净影响 = signed(t) */
}

// mode = reverse：两笔（冲销 + 重录）
func (s *AdjustService) Reverse(old *Transaction, in AdjustInput) ([]*Transaction, int64, error) {
    // ① 冲销：与原交易完全相反方向、同金额
    revert := &Transaction{
        TransactionType: invert(old.TransactionType),   // expense↔income；transfer 走双边
        AmountCents: old.AmountCents, AccountID: old.AccountID,
        CategoryID: old.CategoryID, SubCategoryID: old.SubCategoryID,
        MerchantID: old.MerchantID, Remark: "冲销：" + old.Remark,
        IsAdjustment: 1, AdjustmentType: ptr("reverse"), RelatedTransactionID: &old.ID,
        TransactionTime: nowUnix(),
    }
    // ② 重录：按新值、原方向
    recast := &Transaction{
        TransactionType: old.TransactionType,
        AmountCents: in.NewAmountCents, AccountID: pick(in.NewAccountID, old.AccountID),
        CategoryID: pick(in.NewCategoryID, old.CategoryID),
        SubCategoryID: pickPtr(in.NewSubCategoryID, old.SubCategoryID),
        MerchantID: pickPtr(in.NewMerchantID, old.MerchantID),
        Remark: in.Remark, IsAdjustment: 1, AdjustmentType: ptr("reverse"),
        RelatedTransactionID: &old.ID, TransactionTime: nowUnix(),
    }
    // 净影响 = -oldSigned + newSigned
}
```

**内部转账调整（双边，净 0）**：

```go
func (s *AdjustService) adjustTransfer(group []*Transaction /* out,in 两行 */, in AdjustInput) {
    out, inRow := group[0], group[1]
    delta := in.NewAmountCents - out.AmountCents    // 期望减少的金额
    if delta == 0 { return nil, 0, errNoEffect }
    // 转出账户多扣了 delta → 补回；转入账户多收了 delta → 扣回
    a := &Transaction{ AccountID: out.AccountID, TransactionType: "transfer",
        TransferDirection: "in",  AmountCents: abs(delta),
        TransferGroupID: newGroupID, CounterAccountID: &inRow.AccountID, ... }
    b := &Transaction{ AccountID: inRow.AccountID, TransactionType: "transfer",
        TransferDirection: "out", AmountCents: abs(delta),
        TransferGroupID: newGroupID, CounterAccountID: &out.AccountID,  ... }
    // 两笔同一 transfer_group_id，净影响 = +delta - delta = 0（不污染收支统计）
}
```

**报表处理**：`v_transactions_signed.stat_*` 已排除 `is_adjustment=1`；`adjustment_signed_cents` 单独可加总为"调整净额"。`exclude_adjustments` 参数只影响 `stat_*` 相关聚合。

### 6.8 去重

```go
// 返回命中的已有交易 ID（最多 1 条，给用户看）
func (s *TxnService) FindDuplicate(cand *Transaction) (*Transaction, string, error) {
    // 规则 1：19 位 ID 相同
    if t := find(tx, `SELECT * FROM transactions WHERE id = ? AND deleted_at IS NULL`, cand.ID); t != nil {
        return t, "id", nil
    }
    // 规则 2：(transaction_time, amount_cents, account_id, merchant) 相同
    //   merchant 取值优先级：merchant_id → merchant_text
    if cand.TransactionType == "transfer" { return nil, "", nil }   // 转账不参与规则 2
    q := `SELECT * FROM transactions
           WHERE deleted_at IS NULL AND transaction_time = ? AND amount_cents = ?
             AND account_id = ? AND transaction_type = ?
             AND IFNULL(merchant_id,'') = ? AND IFNULL(merchant_text,'') = ?
           ORDER BY created_seq ASC LIMIT 1`
    if t := find(tx, q, cand.Time, cand.Amount, cand.AccountID, cand.Type,
                 deref(cand.MerchantID), cand.MerchantText); t != nil {
        return t, "time_amount_account_merchant", nil
    }
    // 规则 3（导入专用）：external_id 相同
    if cand.ExternalID != "" {
        if t := find(tx, `SELECT * FROM transactions WHERE external_id = ?`, cand.ExternalID); t != nil {
            return t, "external_id", nil
        }
    }
    return nil, "", nil
}
```
- CSV/OCR 批量入口：`skip_duplicates=true` 时静默跳过；`false` 时强插但记 `warning.duplicate=true`。
- 转账行不参与规则 2（否则两行会互相判重）。

### 6.9 CSV 解析（确定性规则）

**探测顺序**（`csvparse/detect.go`）：

1. 读前 4 字节：`EF BB BF` → UTF-8 BOM；否则尝试 UTF-8 解码，失败 → GBK（`golang.org/x/text/encoding/simplifiedchinese`）。
2. 找表头行：微信账单前 ~16 行是说明文字，表头特征包含 `交易时间` + `交易类型` + `金额(元)`；支付宝包含 `交易时间` 或 `发生时间` + `收/支` 或 `收入` + `支出`。
3. profile 判定：命中 `交易对方`/`商品` → `wechat`；命中 `对方账号`/`商品名称`/`业务流水号` → `alipay`。

**微信映射**（`wechat.go`）：

| 目标字段 | 来源列 | 处理 |
|---|---|---|
| `transaction_time` | 交易时间 | 解析 `2006-01-02 15:04:05` |
| `transaction_type` | 收/支 | `支出`→expense，`收入`→income，`/`（中性）→ 跳过并计数 `skipped` |
| `amount_cents` | 金额(元) | 去 `¥`、`,`；`round(parse*100)`；**必须正数** |
| `merchant_text` | 交易对方 | trim |
| `remark` | 商品 | trim |
| `external_id` | 交易单号 | trim |
| 过滤 | 当前状态 | 含 `已全额退款`/`已退款` 的行默认标记 `warnings`；`交易类型` 为 `零钱提现`/`信用卡还款` 默认建议设为 transfer |

**支付宝映射**（`alipay.go`）：

| 目标字段 | 来源列 | 处理 |
|---|---|---|
| `transaction_time` | 交易时间/发生时间 | 同上 |
| `transaction_type` | 收/支 | 有 `支出金额`>0 → expense；`收入金额`>0 → income |
| `amount_cents` | 金额 / 支出金额 / 收入金额 | 三者择一非零 |
| `merchant_text` | 交易对方 | — |
| `remark` | 商品名称/商品说明 | — |
| `external_id` | 交易订单号/业务流水号 | — |
| 过滤 | 交易状态 | `交易关闭`/`等待付款` → 跳过 |

**金额解析**（`common.go`）：`strings.NewReplacer("¥","",",",""," ","","￥","")` → `strconv.ParseFloat` → `int64(math.Round(v*100))`。**只在解析边界用一次 float**，之后全走整数。

**行指纹**（无 `external_id` 时）：`sha1(localDate|amount|account_id|merchant|type)[:16]` → `external_id`，借 `ux_tx_external` 实现幂等重导。

### 6.10 统计聚合（SQL 粗筛 + Go 分桶）

**为什么不在 SQL 里分桶**：SQLite 的 `strftime` 作用于 `local_date` 文本可行，但"周起始为周一""最近 12 个月连续补零""同比取去年同月"— 这些逻辑在 Go 里写一次即可复用，SQL 里写会失控。

```go
// 1) SQL 只取最小粒度（day）的聚合
rows := query(`
  SELECT local_date,
         SUM(stat_income_cents)  AS inc,
         SUM(stat_expense_cents) AS exp,
         SUM(adjustment_signed_cents) AS adj,
         COUNT(1) AS cnt
  FROM v_transactions_signed
  WHERE local_date BETWEEN ? AND ?  /* + 过滤条件 */
  GROUP BY local_date ORDER BY local_date`)
// 2) Go 按 granularity 折叠
func fold(points []DayAgg, g string) []Bucket {
    // day   : 1:1
    // week  : 以周一为界 (timeutil.WeekStart(t, time.Monday))
    // month : local_date[:7]
    // year  : local_date[:4]
    // 空桶必须补 0（图表 X 轴连续）
}
// 3) 若 granularity = day 且区间 > 92 天，强制降级为 week（避免前端 400+ 点）
```
**高额阈值**：`threshold = max(meta.high_amount_threshold_cents, P95(expense amounts in range))`；若区间支出笔数 < 20 则只用配置值。`P95` 用 `SELECT amount_cents FROM ... ORDER BY amount_cents LIMIT 1 OFFSET ?`（`OFFSET = ceil(n*0.95)-1`）。

**账户余额序列（多折线）**：

```go
// 对每个账户按日推进：先取区间前一日的收盘余额作为起点
startBase := balancesAtDayEnd(accountIDs, startDate.AddDate(0,0,-1))
// 再按日累加当日 net（signed_cents 之和）
for _, d := range days { cur[acc] += dayNet[d][acc]; point[acc][d] = cur[acc] }
// 冻结区直接用物化值更省：若 d < lastFrozenDate，取当日最后一笔 account_balance_after
```

### 6.11 备份与恢复

```go
func (b *BackupService) Create(ctx context.Context) (Info, error) {
    b.mu.Lock(); defer b.mu.Unlock()                    // 与 Restore 互斥
    b.store.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)     // 落盘 WAL
    tmp := filepath.Join(dataDir, "tmp", "ledger.db.snapshot")
    copyFile(dataDir+"/ledger.db", tmp)                 // 主文件已完整，无需 -wal/-shm
    dst := filepath.Join(dataDir, "backups", today(), fmt.Sprintf("ledger-%s.zip", ts()))
    f, _ := os.Create(dst)
    zw := zip.NewWriter(f)
    w, _ := zw.Create("ledger.db"); io.Copy(w, open(tmp))
    zw.Create("manifest.json")  // {app_version, schema_version, created_at, sha256, rows}
    zw.Close(); f.Close()
    checksum := sha256File(dst)
    pruner.Run()                                        // 150 天清理
    meta.Set("last_backup_at", now)
}

func (b *BackupService) Restore(ctx, file string) error {
    // 1. 校验 zip 内 ledger.db 的 sha256 与 manifest 一致
    // 2. 备份当前库到 data/backups/pre-restore-*.zip（安全网）
    // 3. 关闭 gorm 连接（sqlDB.Close()）
    // 4. 解压覆盖 data/ledger.db，删除 -wal/-shm
    // 5. 重新 open + migrate(幂等) + ForceFullRematerialize + 全量校验
}
```
`POST /api/backup/restore` 期间 `middleware.Maintenance` 返回 `503 RESTORING`（用 atomic bool）。

### 6.12 启动任务（`maintenance.go`）

```
顺序（goroutine，串行执行，失败只记 error）：
1. migrate()                     // 幂等 DDL + 视图自检补齐
2. verifySchemaVersion()
3. if meta.frozen_write_flag == 1 → ForceFullRematerialize()
   else if now - last_frozen_at > 24h → Materialize()
4. verify()                      // 全量余额校验；失败 → meta 打标，写操作返回 500 MATERIALIZE_FAILED
5. if weekday == 周一 && last_backup_at 不在本周 → Create()   // PRD 6.7
6. dailyCleanup()                // 截图 30/150 天、OCR 结果 30 天、backup 150 天、import tmp 24h
7. 注册每日 03:00 定时器（物化 + 清理）
```

---

## 7. 前端设计

### 7.1 路由表（`src/router/index.ts`）

| path | name | 组件 | 说明 |
|---|---|---|---|
| `/login` | `login` | `LoginView.vue` | 无 token 时重定向到此；`meta.public = true` |
| `/` | `dashboard` | `DashboardView.vue` | 三段式：摘要卡片 / 图表区 / 明细区 |
| `/transactions` | `transactions` | `TransactionsView.vue` | 全量筛选 + 分页表格 + 回收站 tab |
| `/reports` | `reports` | `ReportsView.vue` | 4 个 tab：分类 / 账户 / 标签 / 周期对比 |
| `/import` | `import` | `ImportView.vue` | 2 个 tab：CSV / 截图 OCR |
| `/settings` | `settings` | `SettingsView.vue` | 8 个 tab（见 §7.3） |
| `/:pathMatch(.*)*` | `notFound` | 重定向 `/` | — |

全局前置守卫：`auth.token` 为空且 `!to.meta.public` → `/login`；有 token → `GET /api/auth/check`（每会话一次）。

### 7.2 页面与组件清单

**布局**

| 组件 | 职责 | 调用 API |
|---|---|---|
| `layouts/AppLayout.vue` | 顶部栏（+ 按钮 / 快速记账入口 / 摘要区入口 / 菜单按钮）+ `<router-view>` + 全局快捷键（`Ctrl+N`/`Alt+N` 唤 `QuickEntryDialog`）+ 深色模式切换 | — |

**Dashboard（`views/DashboardView.vue`）**

| 组件 | 职责 | 调用 API |
|---|---|---|
| `common/StatCard.vue` | 2×2 摘要（收入/支出/结余/总余额，含调整净额小字） | `GET /stats/summary` |
| `common/TimeRangePicker.vue` | 13 个预设 + 自定义；切换后 emit `change` | — |
| `charts/TrendChart.vue` | 折线/柱状切换；`granularity` 切换；点击某日 emit `drill`（基础版只做明细区筛选，不做图表联动） | `GET /stats/trend` |
| `charts/CategoryPieChart.vue` | 环形/饼切换；中心显示总额；按金额降序 | `GET /stats/category-breakdown` |
| `charts/AccountBalanceChart.vue` | 多账户余额折线，账户多选 | `GET /stats/account-balance-series`、`GET /accounts` |
| `charts/CategoryRankingList.vue` | 分类排行 + 展开明细（调 `/transactions`） | `GET /stats/category-ranking`、`GET /transactions` |
| `charts/HighAmountList.vue` | 高额支出列表，可点开详情抽屉 | `GET /stats/high-amount` |
| `common/FilterTagBar.vue` | 活跃筛选条件标签 + 一键清除 | — |
| `transaction/TransactionTable.vue` | 明细表（时间/金额/收支/分类 + 调整项着色），匀速滚动模式（`scroll` prop） | `GET /transactions` |
| `transaction/TransactionDetailDrawer.vue` | 详情抽屉，右上关闭/编辑；冻结区编辑置灰 → "调整"按钮 | `GET /transactions/:id`、`POST /transactions/:id/adjust` |

**Transactions（`views/TransactionsView.vue`）**

| 组件 | 职责 | 调用 API |
|---|---|---|
| `transaction/TransactionFilters.vue` | 账户/分类/标签/商户/金额区间/类型/含调整项/账本 | — |
| `transaction/TransactionTable.vue` | 列：时间、类型、金额、账户、分类、商户、标签、余额、操作；软删除行标灰 | `GET /transactions` |
| `transaction/TransactionEditDialog.vue` | 动态区编辑（含 items、tags）；**保存时遇到 409 FROZEN 自动切到 AdjustDialog** | `PUT /transactions/:id` |
| `transaction/AdjustDialog.vue` | `mode` 单选（adjust/reverse）+ 新金额/新分类/备注（必填，默认模板文案） | `POST /transactions/:id/adjust` |
| `transaction/TransactionItemEditor.vue` | 多物品明细表格（原价/实付/折扣自动算/折扣缘由） | — |
| `transaction/TagSelect.vue` | 多选标签 + 即时新建 | `GET/POST /tags` |
| 回收站（`el-drawer`） | 列出 `include_deleted=true`，恢复 / 彻底删除 | `POST /restore`、`DELETE /purge` |

**记账入口（`components/entry/`）**

| 组件 | 职责 | 调用 API |
|---|---|---|
| `QuickEntryDialog.vue` | 金额（自动 focus）、账户、分类、时间、类型；折叠区（备注/商户/标签/地区）；`Enter` 提交 | `POST /transactions` |
| `TransactionForm.vue` | 完整表单（新增/编辑复用）：全部主字段 + `TransferSection` + items + tags | `POST/PUT /transactions` |
| `TransferSection.vue` | 内部转账（默认折叠，下拉展开）：转出/转入账户 + 金额 | — |
| `TemplateBar.vue` | 常用模板一键填充（PRD 5.5） | `GET/PUT /settings`（`quick_templates`） |

**导入（`views/ImportView.vue`）**

| 组件 | 职责 | 调用 API |
|---|---|---|
| `import/CsvImportPanel.vue` | 拖拽上传 → 预览 → 行选择/字段修正/dup 与 frozen 标记 → 提交 | `POST /import/csv/preview`、`POST /import/csv/commit` |
| `import/CsvPreviewTable.vue` | 可编辑预览表格，`dup` 行黄底、`frozen` 行橙底、`need_confirm` 字段高亮 | — |
| `import/OcrUploadPanel.vue` | 拖拽/粘贴/选择文件；异步轮询任务；批量 | `POST /ocr/recognize`、`GET /ocr/tasks/:id` |
| `import/OcrResultCard.vue` | 识别结果表单（可改），`need_review` 高亮，确认后进 `TransactionForm` | — |

**报表（`views/ReportsView.vue` + `components/reports/`）**

| 组件 | 职责 | 调用 API |
|---|---|---|
| `CategoryReport.vue` | 一级/二级切换、占比、环比/同比、钻取到明细 | `GET /reports/category` |
| `AccountReport.vue` | 各账户收入/支出/转账/当前余额 | `GET /reports/account` |
| `TagReport.vue` | 按标签汇总 + 多标签交叉筛选 | `GET /reports/tag` |
| `PeriodCompareReport.vue` | 月/季/年对比 + 柱状图 + **"排除调整项"开关** | `GET /reports/period-compare` |

**设置（`views/SettingsView.vue` + `components/settings/`）**

| 组件 | 职责 | 调用 API |
|---|---|---|
| `AccountSettings.vue` | 账户 CRUD、归档、拖拽排序、初始余额（冻结后只读提示） | `/accounts/*` |
| `CategorySettings.vue` | 两级分类树 CRUD、颜色、排序 | `/categories/*` |
| `TagSettings.vue` | 标签 CRUD、颜色、使用次数 | `/tags/*` |
| `MerchantSettings.vue` | 商户/平台 CRUD、合并重复（手工选择主项） | `/merchants/*` |
| `LedgerSettings.vue` | 账本 CRUD、默认账本、归档 | `/ledgers/*` |
| `LedgerRuleEditor.vue` | 规则目标多选（分类 l1/l2、标签、账户、商户）+ `match_mode`；保存即重建视图 | `GET/PUT /ledgers/:id/rules` |
| `SystemSettings.vue` | 冻结天数、默认账户/分类/商户、高额阈值、模板管理、深色模式、Token 更换 | `GET/PUT /settings`、`GET /meta` |
| `BackupSettings.vue` | 创建/列表/恢复/删除备份；策略说明 | `/backup/*` |

### 7.3 Pinia stores

```ts
// stores/auth.ts
state: { token: string | null, checked: boolean }
actions: login(token), logout(), check()

// stores/ui.ts
state: {
  theme: 'light' | 'dark',
  timePreset: TimePresetKey,              // 见 §7.4
  customRange: [string, string] | null,
  range: { start: string; end: string },  // 派生，watcher 计算
  trendChartType: 'line' | 'bar',
  pieChartType: 'doughnut' | 'pie',
  trendGranularity: 'day' | 'week' | 'month' | 'year',
  filters: { accountIds: string[]; categoryId: string | null; tagIds: string[];
             merchantId: string | null; type: TransactionType[]; ledgerId: string | null;
             amountMin: number | null; amountMax: number | null;
             includeAdjustments: boolean },
  sidebarCollapsed: boolean
}
getters: rangeQuery()  // → { start_time, end_time } 供所有 API 复用
actions: setPreset(k), setCustom(a,b), toggleTheme(), persist()  // 写 localStorage + ui_prefs

// stores/master.ts
state: {
  accounts: AccountDTO[], categories: CategoryNode[], flatCategories: CategoryDTO[],
  tags: TagDTO[], merchants: MerchantDTO[], ledgers: LedgerDTO[],
  regionsCache: Map<string, RegionDTO[]>,
  loaded: { accounts: boolean; categories: boolean; tags: boolean; ledgers: boolean }
}
getters: activeAccounts, expenseCategories, incomeCategories, subCategoriesOf(id), totalBalanceCents
actions: loadAll(), refresh(kind), upsertLocal(kind, item), removeLocal(kind, id)

// stores/transaction.ts
state: {
  items: TransactionDTO[], total: number, page: number, pageSize: number,
  summary: SummaryDTO | null, loading: boolean,
  query: TransactionQuery,                 // 与 ui.filters 合并后的最终查询
  detail: TransactionDetailDTO | null, drawerOpen: boolean,
  editing: TransactionDTO | null, dialogOpen: boolean,
  deletedItems: TransactionDTO[]           // 回收站
}
actions: fetchList(reset?), fetchDetail(id), create(payload), update(id, payload),
         softDelete(id), restore(id), purge(id), adjust(id, payload), setPage(p)

// stores/stats.ts
state: {
  summary, trend: TrendPoint[], breakdown: BreakdownItem[], breakdownLevel: 1|2,
  series: AccountSeries[], totalSeries: SeriesPoint[],
  ranking: RankItem[], highAmount: { threshold: number; items: TransactionDTO[] },
  loading, lastRangeKey: string
}
actions: fetchAll(range), fetchSummary(), fetchTrend(g), fetchBreakdown(type, level),
         fetchSeries(accountIds, g), fetchRanking(type), fetchHighAmount()

// stores/draft.ts
state: {
  lastAccountId, lastCategoryId, lastMerchantId, lastType: 'expense',
  templates: QuickTemplate[], amountInput: string, collapsed: boolean
}
actions: applyTemplate(t), saveDefaults(), remember(created: TransactionDTO)
```

### 7.4 时间范围预设枚举（`src/utils/presets.ts`）

```ts
export type TimePresetKey =
  | 'this_week' | 'last_2_weeks' | 'last_1_month'
  | 'last_7d' | 'last_15d' | 'last_30d' | 'last_60d' | 'last_90d' | 'last_150d'
  | 'this_month' | 'last_month' | 'this_quarter' | 'this_year'
  | 'last_12_months' | 'custom'

// 语义（全部按 Asia/Shanghai 本地日，含端点）
this_week       → 本周一 00:00:00 ~ 今天 23:59:59        （周一为一周起点）
last_2_weeks    → 今天-13d 00:00:00 ~ 今天 23:59:59
last_1_month    → 今天-29d 00:00:00 ~ 今天 23:59:59
last_7d/15d/30d/60d/90d/150d → 今天-(N-1)d ~ 今天
this_month      → 本月 1 日 ~ 今天
last_month      → 上月 1 日 ~ 上月最后一日
this_quarter    → 本季度首月 1 日 ~ 今天
this_year       → 1 月 1 日 ~ 今天
last_12_months  → 11 个月前的 1 日 ~ 今天
custom          → 用户 el-date-picker（date range）
```
实现全部基于 `dayjs`（`dayjs@^1.11` + `dayjs/plugin/isoWeek`），**不用** `new Date()` 手算，避免时区坑。默认预设 `last_30d`。

### 7.5 ECharts 配置要点（`src/utils/echarts.ts` + 各 chart 组件）

```ts
// 统一：注册 + 调色板 + 主题跟随
import * as echarts from 'echarts'
export const PALETTE = ['#F2994A','#2D9CDB','#27AE60','#EB5757','#9B51E0',
                        '#F2C94C','#56CCF2','#6FCF97','#BB6BD9','#828282']
export const ADJ_COLOR = '#BB6BD9'   // 调整/冲销专属色（PRD 4.8）
```

| 图表 | 关键配置 |
|---|---|
| **趋势图** `TrendChart` | `xAxis.type='category'`（`label` 用 `MM-DD`/`W35`/`2026-09`/`2026`）、`yAxis.type='value'` 且 `axisLabel.formatter = v => (v/100).toFixed(0)`；两条 series（收入绿 `#27AE60`、支出红 `#EB5757`）+ 一条 `line` 叠加"调整净额"（虚线、`ADJ_COLOR`）；`tooltip.trigger='axis'` 且 `formatter` 把分转元；`grid: {left: 8, right: 8, top: 32, bottom: 8, containLabel: true}`；`dataZoom` 区间 > 60 点时启用；`onEvents.click` → emit `drill(date)` |
| **分类占比** `CategoryPieChart` | `series.type='pie'`、`radius=['52%','72%']`（环形）/`'68%'`（饼）、`center=['50%','48%']`、`itemStyle.borderRadius=4`、`label.show=false`、`labelLine.show=false`；中心总额用 `graphic` 或 `title.subtext`（`¥8,300.50`）；`tooltip.formatter` 显示 `名称 / 金额 / 占比`；颜色按 category 固定映射（`master.categories` 的 `color` 优先，缺省取 `PALETTE[i]`） |
| **账户余额多折线** `AccountBalanceChart` | `xAxis` 同上；`series[].type='line'`、`smooth=false`（余额不要平滑）、`symbol='none'`、`connectNulls=true`；`legend` 可滚动（`type='scroll'`）；**账户数 > 6 默认只显示前 5 + 总计**；总余额用粗线 + `areaStyle` 半透明 |
| **分类排行** `CategoryRankingList` | 非 ECharts，`el-table` + 行内 `el-progress`（`percentage = amount/max*100`，`stroke-width=6`，颜色取分类色）；展开行调 `/transactions?category_id=` |
| **高额** `HighAmountList` | 非 ECharts，`el-table`（金额列 `MoneyText` 加粗红字）；顶部展示"当前阈值 ¥500（自动 P95）" |
| **周期对比** `PeriodCompareReport` | `series.type='bar'`、`barGap='10%'`、两组（本期/上期）× 两系列（收入/支出）；`label.show=true` 且 `position='top'` |

**通用要求（PRD 4.9）**

- 容器必须有明确宽高（`.chart { width: 100%; height: 320px; }`），`BaseChart.vue` 在 `onMounted` 里 `echarts.init(el)`，`watch(option, setOption, {deep:true})`，`ResizeObserver` → `chart.resize()`，`onBeforeUnmount` → `dispose()`。
- 深色模式切换：`watch(theme)` → `chart.setOption({ backgroundColor: 'transparent' })` + 重新 `init` 或用 `echarts.registerTheme` + `setOption` 重设 `textStyle.color`。**推荐**：`vars.css` 里的 `--chart-text` 变量，通过 `getComputedStyle` 读取后注入 option，无需重建实例。

### 7.6 深色模式与主题变量

`src/styles/vars.css`：

```css
:root {
  --bg-page: #f5f6f8; --bg-card: #ffffff; --bg-elevated: #ffffff;
  --text-1: #1f2328; --text-2: #57606a; --text-3: #8b949e;
  --border-1: #d0d7de; --border-2: #e6eaef;
  --accent: #0969da; --accent-bg: #ddf4ff;
  --income: #1a7f37; --expense: #cf222e; --adjust: #8250df;
  --chart-text: #57606a; --chart-split: #e6eaef;
  --radius-card: 12px; --shadow-card: 0 1px 3px rgba(31,35,40,.08);
}
html.dark {
  --bg-page: #0d1117; --bg-card: #161b22; --bg-elevated: #1c2128;
  --text-1: #e6edf3; --text-2: #9198a1; --text-3: #6e7681;
  --border-1: #30363d; --border-2: #21262d;
  --accent: #2f81f7; --accent-bg: #12263f;
  --income: #3fb950; --expense: #f85149; --adjust: #a371f7;
  --chart-text: #9198a1; --chart-split: #21262d;
  --shadow-card: 0 1px 3px rgba(1,4,9,.6);
}
body { background: var(--bg-page); color: var(--text-1); }
```

`src/styles/index.scss` 里覆盖 Element Plus 关键变量（`--el-bg-color`、`--el-text-color-primary`、`--el-border-color`…）映射到上面的变量；`main.ts` 里 `import 'element-plus/theme-chalk/dark/css-vars.css'`，`ui.toggleTheme()` 只改 `<html class="dark">` + 写 `localStorage.theme` + 调 `PUT /api/settings {theme}`。

**说明**：Element Plus 的 dark 变量与本项目变量**双向映射**（用 `--el-*` 指向自有变量，而不是反过来），保证图表与组件同源。

**对比度与字号（PRD 4.8）**：`--text-2`/`--text-3` 已按 ≥4.5:1 在对应底色上验证；中文正文 ≥14px，辅助文字 ≥12px，表格 `font-size: 13px`。

### 7.7 交互约定

| 场景 | 行为 |
|---|---|
| 时间范围切换 | `ui.setPreset()` → `ui.range` 变 → `stats.fetchAll()` + `transaction.fetchList(true)`（`watch` 深度监听，500ms 防抖） |
| 筛选 | `ui.filters` → `FilterTagBar` 显示可删除标签；`transaction.query` 由 `computed` 合成 |
| 冻结区交易 | 表格行左侧 3px 橙条 + `el-tag` "冻结"；详情抽屉"编辑"置灰并显示 Tooltip + "调整"主按钮 |
| 调整项交易 | 金额用 `--adjust` 色 + `el-tag` "调整"/"冲销"；hover 显示 `related_transaction_id` 跳转 |
| 保存反馈（PRD 5.9） | `ElMessage.success` + `ElMessageBox` 撤销（撤销 = `DELETE` 刚建的那笔）；若响应 `warning.frozen` → 附"该笔已进入冻结区，不可直接修改"；`warning.duplicate` → 弹确认框 |
| 图表下钻（基础版范围） | 点击趋势图某日 → 明细区筛选该日（**仅明细区过滤，不做跨图联动**，PRD 4.3 明确基础版不做） |
| 快捷键 | `Ctrl+N` / `Alt+N` → `QuickEntryDialog`；`Esc` 关闭；`Ctrl+Enter` 保存 |
| 空态 | `EmptyState.vue` 统一文案 + 引导按钮 |
| 错误提示 | `axios` 拦截器：`error.code` → 中文文案表（§10.1）；`FROZEN` 自动引导到 `AdjustDialog` |

---

## 8. OCR 服务设计（Python）

### 8.1 进程与接口

监听 `127.0.0.1:8771`，`uvicorn app.main:app --host 127.0.0.1 --port 8771 --workers 1`（模型常驻内存，多 worker 会重复加载）。

| Method | Path | 说明 |
|---|---|---|
| GET | `/healthz` | 进程存活 |
| GET | `/readyz` | `{ready, engine, models:[]}` 模型是否加载完成 |
| POST | `/api/v1/recognize` | `{image_path 或 image_base64, model:"auto|heavy|light|mock", mode:"sync|async", hint:"wechat|alipay|receipt"}` |
| POST | `/api/v1/recognize/batch` | `{images:[...], model}` → `{task_ids:[]}` |
| GET | `/api/v1/tasks/{task_id}` | `{status, progress, result, error, elapsed_ms}` |
| GET | `/api/v1/model` | `{current, available:[{name,engine,loaded,params}], healthy}` |
| POST | `/api/v1/model/switch` | `{name}` → 用户需先 `unload` 当前（`{name, unload_first:true}`） |

### 8.2 引擎抽象（`app/engines/base.py`）

```python
class BaseEngine(ABC):
    name: str
    @abstractmethod
    async def load(self) -> None: ...
    @abstractmethod
    async def unload(self) -> None: ...
    @abstractmethod
    async def health(self) -> bool: ...
    @abstractmethod
    async def recognize(self, image: PIL.Image.Image, hint: str) -> dict:
        """返回与 PRD 7.3 一致的结构化 dict（含 confidence / raw_text / model_used）"""
```

| 引擎 | 实现要点 |
|---|---|
| `MockEngine` | 正则 + 版面启发式：找 `¥?\d+\.\d{2}` 作金额、`\d{4}-\d{2}-\d{2} \d{2}:\d{2}` 作时间、识别"支出/收入/收款/付款"关键词定方向、取含"店/公司/超市/餐厅"的最长文本作商户。**永远 `need_review=True`、`confidence` 全 0.3**。无模型时的兜底，保证主流程可用 |
| `OllamaEngine` | `POST {base}/api/chat`，`{model, messages:[{role:"system",content:PROMPT},{role:"user",content:..., images:[b64]}], stream:false, format:"json"}`；超时 120s；失败重试 1 次 |
| `OpenAIVLEngine` | `POST {base}/v1/chat/completions`，`image_url` 传 `data:image/jpeg;base64,...`；适配 vLLM / LM Studio / 任意 OpenAI 兼容端点 |

**热切换（`registry.py`）**：

```python
class EngineRegistry:
    def __init__(self): self._current: BaseEngine | None = None; self._lock = asyncio.Lock()
    async def switch(self, name: str, unload_first: bool = True) -> dict:
        async with self._lock:                      # 切换期间 recognize 阻塞等待
            if self._current and unload_first: await self._current.unload()
            eng = build(name)                       # 工厂：'mock' / 'ollama:<model>' / 'openai:<base>/<model>'
            await eng.load()                        # 失败 → 保留 mock 并返回 error
            self._current = eng
            return {"current": eng.name, "ok": True}
    async def get(self) -> BaseEngine:
        async with self._lock:
            return self._current or self._fallback   # _fallback = MockEngine()（启动时即加载）
```

**启动策略**：`lifespan` 里先加载 `MockEngine`（`ready=true`），再按 `OCR_DEFAULT_ENGINE` 环境变量尝试加载真实引擎；失败**只打 warn**，不阻止启动。这样 Go 端 `GET /api/ocr/model` 永远可用，`recognize` 在无模型时返回 `mock` 结果 + `need_review=true`。

### 8.3 处理流水线（`app/pipeline/`）

```
preprocess: PIL 打开 → EXIF 转正 → 长边 > 2048 等比缩放 → 转 RGB → （可选）灰度/对比度增强 → JPEG q=88 bytes
prompt:     按 hint 选提示词（wechat / alipay / receipt），要求"仅输出 JSON"，给出字段清单与示例
engine:     调模型拿原始文本
postprocess: ① 抽取第一个完整 JSON（括号配对扫描，容忍 ```json 包裹）
             ② 字段规整：金额 → 分（int）；时间 → 'YYYY-MM-DD HH:mm:ss'；收支 → expense/income
             ③ 缺失字段填 null，不猜（交由 need_review）
rules:      PRD 7.6 七条：金额>0 且 ≤2 位小数 / 时间合法 / 账户存在 / 分类存在 /
             收支方向与类型一致 / 重复检测 / 商户在线平台校验 / 冻结区提示
             → 返回 warnings[]，任一不通过则 need_review=True
```
> "账户存在/分类存在/重复检测"需要数据库数据 → **这三条由 Go 端在 `/api/ocr/tasks/:id` 返回后、入账前校验**（Python 不连数据库）。Python 只做纯文本规则（1、2、5）与格式化。这条边界必须写清，避免工程师重复实现。

### 8.4 任务与性能

- 内存任务表 `dict[task_id, Task]` + `asyncio.Queue`（并发 1，避免抢显存）；TTL 1h，`/tasks` 过期返回 404。
- 每个任务记录 `elapsed_ms`、`engine`、`ok`，`GET /api/v1/model` 返回累计 `stats: {count, avg_ms, p95_ms, fail_rate}` 供 PRD 7.2"性能监控"。
- 结构化结果同时写 `data/ocr-results/YYYY-MM-DD/{id}.json`（由 Go 端写，Python 只返回 JSON，**不碰文件系统**——路径由 Go 传入，Python 只读图片）。

### 8.5 requirements.txt

```
fastapi==0.115.*
uvicorn[standard]==0.32.*
pydantic==2.9.*
httpx==0.27.*
pillow==11.0.*
python-multipart==0.0.12
```
> 无 torch/transformers；`httpx` 指向 Ollama/vLLM。安装体积 < 60MB，3 秒内启动。

---

## 9. 部署与运维

### 9.1 数据目录（运行时，`data/` 在工作目录下）

```
data/
├── ledger.db                  # 唯一数据库文件（WAL）
├── ledger.db-wal / -shm       # SQLite 自动管理
├── screenshots/
│   ├── originals/YYYY-MM-DD/{19位ID}.jpg     # 保留 30 天
│   └── compressed/YYYY-MM-DD/{19位ID}.jpg    # 保留 150 天
├── ocr-results/YYYY-MM-DD/{19位ID}.json      # 保留 30 天
├── backups/YYYY-MM-DD/ledger-YYYYMMDD-HHmm.zip  # 保留 150 天
├── tmp/import/{batch_id}.json                # 保留 24h
└── logs/ledger.log                           # 按日切割，保留 30 天
```

### 9.2 运行方式

**开发（`scripts/dev-backend.ps1` / `dev-frontend.ps1`）**

```powershell
# 后端
$env:CGO_ENABLED=0; go run ./cmd/server        # → 127.0.0.1:8770
# 前端
cd web; npm run dev                            # → 127.0.0.1:5173，/api 代理到 8770
# OCR
cd ocr-service; uvicorn app.main:app --port 8771
```

**构建（`scripts/build.ps1`）**

```powershell
cd web; npm ci; npm run build                  # → web/dist
$env:CGO_ENABLED=0; go build -trimpath -ldflags "-s -w -X main.version=1.0.0" -o ledger.exe ./cmd/server
```

**单机运行**：`./ledger.exe`，浏览器打开 `http://127.0.0.1:8770`。`--data-dir`、`--port`、`--ocr-endpoint` 命令行参数覆盖。

### 9.3 docker-compose.yml

```yaml
services:
  backend:
    build: { context: ., dockerfile: Dockerfile.backend }
    network_mode: host                 # PRD 6.3 建议 host 模式
    environment:
      LEDGER_DATA_DIR: /data
      LEDGER_PORT: "8770"
      LEDGER_OCR_ENDPOINT: http://127.0.0.1:8771
      LEDGER_TZ: Asia/Shanghai
    volumes: [ "./data:/data" ]
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "/app/ledger", "-healthcheck"]
      interval: 30s

  ocr:
    build: { context: ./ocr-service, dockerfile: Dockerfile }
    network_mode: host
    environment:
      OCR_PORT: "8771"
      OCR_DEFAULT_ENGINE: "mock"        # 或 ollama:qwen2.5vl:7b
      OLLAMA_BASE_URL: "http://127.0.0.1:11434"
    volumes:
      - "./data:/data:ro"               # 只读挂载，Python 只读图片
      - "./ocr-service/models:/models:ro"
    restart: unless-stopped
```
`Dockerfile.backend`：`golang:1.23-alpine` 构建（`CGO_ENABLED=0`）→ `alpine` 运行 + `ca-certificates` + `tzdata`；镜像 < 20MB。
`Dockerfile.web`：本基础版**不需要**（后端直接托管 `web/dist`，`Dockerfile.web` 仅用于多阶段构建中产出静态资源并 COPY 进 backend 镜像）。

### 9.4 清理与保留（PRD 6.6 / 8.7）

| 数据 | 保留 | 清理实现 |
|---|---|---|
| `screenshots/originals/<date>/` | 30 天 | `os.RemoveAll` 目录（按目录名日期判断） |
| `screenshots/compressed/<date>/` | 150 天 | 同上 |
| `ocr-results/<date>/` | 30 天 | 同上 |
| `backups/<date>/*.zip` | 150 天 | 按目录名日期 |
| `tmp/import/*.json` | 24h | 按 mtime |
| 软删除交易 | 6 个月 | 不自动删（保留可恢复），仅 `purge` 时校验 `deleted_at > 180d` |
| `logs/ledger.log*` | 30 天 | — |

自动清理**不需要用户确认**（PRD 8.7），由启动任务 + 每日 03:00 定时器执行。

### 9.5 版本升级与回滚（PRD 6.9）

- `schema_version` 存 `meta`；`migrate.go` 按版本号顺序执行迁移块，**升级前自动 `Backup.Create()`**。
- 回滚 = `POST /api/backup/restore` 选升级前的包（备份文件名含时间，可"按时间点恢复"）。
- 不静默覆盖：`restore` 必须 `confirm:true`，且先把当前库另存为 `pre-restore-*.zip`。

---

## 10. 共享约定（跨文件强制）

### 10.1 错误码表

| code | HTTP | 场景 | 前端处理 |
|---|---|---|---|
| `VALIDATION_ERROR` | 400 | 字段校验失败（`details.fields`） | 表单字段标红 |
| `UNAUTHORIZED` | 401 | 缺/错 token | 跳登录 |
| `BAD_TOKEN` | 401 | 登录 token 错误 | 提示 |
| `NOT_FOUND` | 404 | 资源不存在 | 提示 + 刷新列表 |
| `FROZEN` | 409 | 写冻结区交易 | 引导 AdjustDialog |
| `DUPLICATE` | 409 | 命中去重（`details.matched_transaction_id`） | 确认框"仍要导入？" |
| `IN_USE` / `ACCOUNT_IN_USE` / `CATEGORY_IN_USE` | 409 | 被引用不能删 | 提示改归档 |
| `TRANSFER_IMMUTABLE` | 400 | 改转账金额/账户 | 提示走调整 |
| `NO_EFFECT` | 400 | 调整后金额不变 | 提示 |
| `RETENTION` | 409 | purge 时未满 180 天 | 提示 |
| `SEQUENCE_OVERFLOW` | 500 | 月度序列耗尽 | 提示联系维护 |
| `MATERIALIZE_FAILED` | 500 | 物化/校验失败 | 全屏告警，禁用写操作 |
| `OCR_UNAVAILABLE` | 503 | Python 服务不可达 | 降级手动录入 |
| `OCR_TASK_NOT_FOUND` | 404 | 任务过期 | 重新上传 |
| `UNSUPPORTED_CSV` | 422 | 无法识别账单格式 | 提示手动选择来源 |
| `IMPORT_BATCH_EXPIRED` | 410 | 批次超过 24h | 重新上传 |
| `RESTORING` | 503 | 恢复中 | 全屏 loading |
| `BACKUP_FAILED` | 500 | 备份/恢复失败 | 提示 + 日志路径 |
| `INTERNAL` | 500 | 兜底 | 提示 + `details.request_id` |

### 10.2 分页

请求：`page`（默认 1）、`page_size`（默认 50，上限 500）。
响应：`{ total: number, page: number, page_size: number, items: T[] }`。**列表端点一律此结构**（含 `accounts`/`tags`，即使不分页也返回 `total`）。

### 10.3 时间

- DB：`transaction_time` 等全部 `INTEGER` UTC 秒。
- API 出入：`YYYY-MM-DD HH:mm:ss`（Asia/Shanghai，无时区后缀）。
- 纯日期参数（`start_time=2026-09-01`）→ 后端补 `00:00:00`；`end_time=2026-09-30` → 补 `23:59:59`。
- 前端展示：`datetime.ts` 统一格式化，禁止在组件里手写 `toLocaleString`。

### 10.4 枚举值

| 字段 | 取值 |
|---|---|
| `transaction_type` | `expense` \| `income` \| `transfer` |
| `adjustment_type` | `adjust` \| `reverse` |
| `transfer_direction` | `out` \| `in` |
| `account_type` | `asset`（仅此一种，未来扩展） |
| `value_type`（储值类型） | `wallet`(零钱包) \| `wealth`(理财) \| `yuebao`(余额宝) \| `current`(活期) \| `fixed`(定期) \| `other` |
| `merchant_type` | `offline` \| `online` |
| `source` | `manual` \| `csv` \| `ocr` \| `adjustment` \| `import` |
| `ocr_status` | `pending` \| `processing` \| `done` \| `failed` \| `skipped` |
| `match_mode` | `or` \| `all` |
| `target_type` | `category_l1` \| `category_l2` \| `tag` \| `account` \| `merchant` |
| `granularity` | `day` \| `week` \| `month` \| `year` |
| `sort` | `time_desc` \| `time_asc` \| `amount_desc` \| `amount_asc` \| `created_desc` |

### 10.5 金额

- 传输与存储一律 `*_cents` 整数。
- 前端只允许在"展示"和"输入解析"两处做 `cents ↔ yuan`：`money.ts` 的 `formatCents(c)` / `parseYuan(str)`。
- `parseYuan` 用 `Math.round(parseFloat(s) * 100)`，非法输入返回 `null`。

### 10.6 种子数据（`store/seed.go`）

| 类别 | 内容 |
|---|---|
| 支出分类（一级） | 餐饮、交通、居住、购物、娱乐、医疗健康、教育、人情、金融、其他（含常用二级：午餐/晚餐/早餐、地铁/公交/打车/加油、房租/水电/物业、日用/服饰/数码、电影/游戏/旅行、门诊/药品/体检、书籍/课程/培训、红包/礼物、手续费/利息、其他） |
| 收入分类（一级） | 工资、奖金、兼职/外快、投资理财、退款/报销、红包、其他 |
| 标签 | 报销、可报销、必要、非必要、大额、冲动消费、出差、旅行、家庭、宠物、学习、健康 |
| 商户（online） | 淘宝、京东、拼多多、美团、饿了么、滴滴、微信、支付宝、抖音、盒马 |
| 账户 | 招商银行借记卡1（1234, current, 初始 0）、支付宝余额宝（yuebao）、微信零钱（wallet） |
| 账本 | 日常（`is_default=1`，规则：全部一级分类 or 全部标签）、旅行（规则：标签=旅行）、出差（规则：标签=出差） |
| 地区 | 县级以上行政区划。**实现方式**：`store/regions_seed.json`（`go:embed`）按需导入；若文件缺失则只插 34 个省级 + 常用城市（约 60 条），并在 README 标注"完整地区库由 `scripts/import-regions.ps1` 生成"。避免 3000 行硬编码阻塞开发 |

### 10.7 日志

`slog` JSON，字段：`ts, level, msg, req_id, path, method, dur_ms, err_code`。SQL 慢查询（>200ms）单独 `warn`。日志不打印 `auth_token`、`card_tail` 完整值。

---

## 11. 未决问题与假设

| # | 问题 | 假设/决策 | 影响 |
|---|---|---|---|
| 1 | **地区库 3000 条县级以上数据源** | 基础版先内置省级 + 常用城市（~60 条），提供 `regions_seed.json` 覆盖机制 + `scripts/import-regions.ps1` 从 CSV 导入；不联网抓取（PRD 8.8 不联网） | 用户需手动导入一次完整地区库；T02 只做结构与导入器 |
| 2 | **CSS 方案偏离团队默认 Tailwind** | 用 CSS 变量 + SCSS。理由见 §1.2 | 若团队强要求 Tailwind，可共存（Tailwind 只管布局类），但主题变量仍走 CSS 变量 |
| 3 | **Element Plus 全量引入** | 认可 ~350KB gzip；本地部署无带宽压力 | 若需优化，后续加 `unplugin-vue-components` |
| 4 | **图片压缩未实现** | 基础版把原图字节复制到 `compressed/` 目录，接口预留 `Compressor` | 与 PRD "压缩后截图保留 150 天"语义一致（有文件、有独立保留期），只是体积未减。若用户在意磁盘，T07 后续接 `disintegration/imaging` |
| 5 | **`is_count_in_total` 与信用卡/负债** | PRD 已移除信用卡/负债，`account_type` 只留 `asset` | 未来加 `liability` 时，总余额公式改为 `Σ asset − Σ liability`，需改 §6.5 一处 SQL |
| 6 | **`transfer` 与账本规则** | 转账行也参与账本匹配（按 account 规则），但不参与分类/标签规则（无分类） | 账本里会看到转账，符合"该账本下所有交易"语义 |
| 7 | **词条自动纳入（PRD 1.9 手动写入超 10 次自动纳入）** | 基础版**不自动创建**商户，只做 `use_count` 累加 + 设置页显示"候选词条（≥10 次）"由用户确认创建 | 避免脏数据；与 PRD "需支持去重与确认"一致 |
| 8 | **`reverse` 生成的两笔是否都标 `adjustment_type=reverse`** | 是，都标 `reverse`，靠 `remark` 前缀"冲销："区分 | 报表"调整净额"= 两笔之和，正确 |
| 9 | **冻结区写入（强制导入历史）的代价** | 允许，但触发 `ForceFull` 全量重物化（10 万行 ~2s） | 导入大批历史账单时可能卡顿数秒；commit 分批（每批 500 行）以控制单次事务大小 |
| 10 | **`freeze_days` 可配置（PRD 10.9 列为后续）** | 已在 settings 中开放（10–365，步长 5），但**只影响新边界**，不重算已物化区间 | 调大 freeze_days 会让部分已物化交易"解冻"回到动态区 —— 此时 `account_balance_after` 仍存在但不被读取（因为动态区用窗口算），结果正确；调小则反之，会触发一次正常物化推进 |
| 11 | **OCR 结果里账户/分类匹配** | Python 只返回文本名，Go 端按 `name` 精确匹配 → `category_id`；匹配不到则 `null` + `need_review` | 不做模糊匹配（容易错），前端让用户选 |
| 12 | **多物品 `discount_cents` 为生成列** | SQLite `GENERATED ALWAYS AS ... VIRTUAL`，GORM 写入时须 `Omit("discount_cents")` | 若不 Omit 会报错；已在 §4.1 模型注释标注 |
| 13 | **时区统一** | 全库只支持 `Asia/Shanghai`，`config.TZ` 固定；DB 存 UTC 秒 | PRD 2.13 2-J 已确认 |
| 14 | **`local_date` 冗余字段一致性** | 由 service 层在写入/修改时统一由 `transaction_time` 推导，禁止手工传 | 若不一致会导致按日聚合错位；T04 需单测覆盖 |

---

**文档结束。任务分解见 `docs/TASKS.md`。**
