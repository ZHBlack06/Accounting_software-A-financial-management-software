# 后端开发笔记（T01–T08）

## 模块结构
- `cmd/server/main.go`：装配 + §6.12 启动 7 步（migrate → seed/视图自检/schema_version → 物化补偿 → 全量校验 → 周备份 → 清理 → HTTP）+ 每日 03:00 定时器（物化/清理/周一备份）。
- `internal/config`：`LEDGER_HOST`(127.0.0.1) / `LEDGER_PORT`(8770) / `LEDGER_DATA_DIR`(./data) / `OCR_BASE_URL`(http://127.0.0.1:8771，兼容 `LEDGER_OCR_ENDPOINT`)，均有同名 `--flag`；时区 Asia/Shanghai（Windows 无 tzdata 回退 +08:00）。
- `internal/store`：GORM 主数据 CRUD + 手写热点 SQL；`WriteTx` 单连接事务；`Store.Reopen()` 供备份恢复换库。
- `internal/service`：balance / materialize / transaction / **adjust**（adjust|reverse|转账双边）/ **stats**（summary/trend/breakdown/series/ranking/high-amount）/ **report**（category/account/tag/period-compare）/ **import**（两阶段）/ **ocr**（代理+后处理）/ **backup**（zip+restore）/ **screenshot**（19 位 ID+双份落盘）/ **maintenance**（清理）。
- `internal/pkg/csvparse`：BOM/UTF-8/GBK 探测、表头定位、微信/支付宝 profile、金额/时间解析、行指纹。
- `internal/http`：71 条路由（69 契约端点 + `POST /api/import/commit` 别名 + 认证分组）；静态托管 `web/dist` SPA fallback。

## 启动
```
CGO_ENABLED=0 go build -o ledger.exe ./cmd/server && ./ledger.exe
# docker: LEDGER_HOST=0.0.0.0 LEDGER_DATA_DIR=/app/data OCR_BASE_URL=http://ocr:8771
```
Token：`go run ./scripts/gettoken.go`（meta 表 `auth_token`，64 hex）。请求头 `Authorization: Bearer <token>`。

## 验证
- `go test ./... -count=1` 全绿：ids / timeutil / service（余额 7 例 + adjust 5 例 + verify 1 例）/ csvparse 4 例。
- `python scripts/smoke.py`（服务已启动）：**146 项断言全过**（真实 OCR 在线/离线两种形态均验证）；QA 的 `scripts/e2e-verify.py` 42/42（OCR down）+ 7/7（OCR up）。
- `python scripts/demo_seed.py`：写入 11 笔演示交易（近 20 天，含转账对），当前 `data/ledger.db` 已含，余额 1268700/151500/73500，total=1493700。

## OCR 代理（按 ocr-service 精确契约修定）
1. `hint`：为空不下发；非空归一化为 `wechat`/`alipay`/`receipt`（中文「微信/支付宝/收据」同样识别），未知值省略（服务端默认 receipt）。
2. 非 2xx 一律失败：4xx 与 5xx 均 → **502 `OCR_UPSTREAM_ERROR`**（details 带 `status`/`upstream_code`，message 透传 `error.code/message`）；仅网络不可达（连不上）→ 503 `OCR_UNAVAILABLE`。
3. 解包：sync `{success,data}`、task `{result}`、否则根对象；`code != 0`、`success:false`、缺关键字段（transaction_time/amount_cents/amount）均判失败，不再假成功写 `done`。
4. `GET /api/ocr/tasks/:id`：`status=failed` 或 `error` 非空 → 502 `OCR_UPSTREAM_ERROR`（details 带 task_id/status），截图置 `failed`，绝不返回 done。
5. `ScreenshotStore.Abs` 用 `filepath.Abs`（`LEDGER_DATA_DIR=./data-qa2` 相对路径下也发绝对路径）。
6. 缺 `task_id` → 502（原为 500）。
7. 新增 `internal/service/ocr_test.go`（8 例，httptest 按真实契约）：空 hint 不下发、hint 归一化、4xx 报错+状态落 failed、`success/data` 解包、`code!=0` 报错、缺关键字段报错、缺 task_id 502、failed 任务报错、done 任务解包回填。
8. `scripts/smoke.py` 假 OCR 改为真实契约形状（`{success,data}` / `{task_id,status,result,error}`），并支持 `SMOKE_BASE`/`SMOKE_DATA_DIR` 环境变量。
9. `scripts/gettoken.go`、`scripts/demo_seed.py` 统一支持 `LEDGER_DATA_DIR`（不再写死 `data/`）；gettoken 在库未初始化时给可读错误而非 panic。
复验：真实 uvicorn 8771 → sync 200（model_used=mock, need_review=true，mock 引擎无模型故字段为空并带 7 条中文 warnings）、async 202→轮询 done；停 OCR → 503 `OCR_UNAVAILABLE` 且后端可用；`go test ./...` 全绿、`go vet` 干净、smoke 146/146。

## OCR 联调
1. `cd ocr-service && uvicorn app.main:app --port 8771`（或 `OCR_DEFAULT_ENGINE=mock` 兜底）。
2. 后端 `OCR_BASE_URL` 指向该地址；未启动时 `/api/ocr/recognize`、`/api/ocr/model` 返回 503 `OCR_UNAVAILABLE`，`/api/health.ocr=unavailable`。
3. 转发时传**绝对路径**（DataDir + screenshots/originals/...），容器内即 `/app/data/...`；Go 侧做分类/账户/商户名→id 回填与冻结区提示。

## 与契约的偏差
1. `regions_seed.json` 放 `internal/store/`（go:embed 不能跨包；§10.6 为准）。
2. 收入分类「其他」改名「其他收入」（`ux_categories_name` 与种子清单冲突，schema 为准）。
3. 新增 `GET /api/ledgers/:id`（T03.7 需要）。
4. `frozen_write_flag=1` 时 `EnsureFresh` 立即 ForceFull，不受 30s 节流（更严格）。
5. purge 未软删交易 → 400 `VALIDATION_ERROR`（契约只定义未满 180 天 → 409 RETENTION）。
6. 转账转出行 `total_balance_after` 逐笔累计会瞬时下降、转入行恢复（§6.4 逐笔语义）。
7. `GET /api/stats/account-balance-series` 的 `total_points` 恒为全部 `is_count_in_total=1` 账户之和（含归档），与 `/accounts.total_balance_cents` 同口径。
8. 转账 + `reverse` 模式 → 400 `VALIDATION_ERROR`（§6.7 只定义转账 adjust 双边）。
9. CSV 微信「零钱提现/信用卡还款」仅给 warning（无法自动配对转账），不自动转 transfer。
10. 行指纹/去重以预览时传入的 `account_id`（或默认账户）计算。
11. 备份安全网直接生成 `pre-restore-*.zip`（Windows 下 rename 会被文件占用阻塞）。
12. `golang.org/x/text` v0.21.0（离线缓存版本）。

## 已知限制
- 截图「压缩」为字节复制（§11 假设 4）；OCR 任务映射为进程内存（TTL 随进程）。
- 地区库为省级+常用城市/区 60 条（§11 假设 1）。
- `POST /api/reports/...` 全部为 GET；模板管理复用 `/api/settings.quick_templates`（契约端点表未单列）。

---

# v1.2 后端开发笔记（T01–T05）

## 新增依赖

| 模块 | 版本 | 说明 |
|---|---|---|
| `github.com/xuri/excelize/v2` | **v2.8.1** | 纯 Go、无 cgo；读取 xlsx 的 sharedStrings / styles / sheet rels / inlineStr / 公式缓存值 / date1904 |
| `github.com/xuri/efp` | v0.0.0-20231025114914-d1ff6096ae53 | excelize 依赖（间接） |
| `github.com/xuri/nfp` | v0.0.0-20230919160717-d98342af3f05 | 同上 |
| `github.com/mohae/deepcopy` | v0.0.0-20170929034955-c48cc78d4826 | 同上 |
| `github.com/richardlehane/mscfb` / `msoleps` | v1.0.4 / v1.0.3 | 同上 |

一次性拉取（有网）：
```
GOPROXY=https://goproxy.cn,direct GOSUMDB=off go get github.com/xuri/excelize/v2@v2.8.1
```
之后离线可复现：`GOFLAGS=-mod=mod GOSUMDB=off GOPROXY=off go build ./...`（已实测通过）。
`proxy.golang.org` 在本机不可达（502），**必须**用 `goproxy.cn`。

## 新增文件

| 文件 | 作用 |
|---|---|
| `internal/pkg/sheetparse/{types,valueparse,distribute,detect,xlsx,csv,probe,parse}.go` | 解析内核（纯函数，无 DB/网络） |
| `internal/service/import_sheet.go` | probe/preview/commit 编排 + `manifest.json` / `rows-preview.jsonl` |
| `internal/service/auto_create.go` | `EnsureCategoriesTx` / `EnsureTagsTx`（批内缓存 + 唯一索引回查） |
| `internal/service/account_merge.go` | 账户合并 + v1.2 首次启动自动合并迁移 |
| `internal/store/import_batch_repo.go` | `import_batches` CRUD / `MarkCommitted` 幂等守卫 |
| `internal/http/handlers_import_sheet.go` | 3 个端点 + `GET /api/import/batches` |
| `web/embed.go` | **补文件**：`cmd/desktop` 依赖 `ledger/web` 内嵌 web/dist，v1.2 目录缺失导致 `go build ./...` 失败；内容与 v1.1 完全一致，未改任何前端代码 |

改动文件：`internal/store/{schema.sql,migrate.go,models.go,seed.go,tx_repo.go,master_repo.go}`、
`internal/service/transaction.go`（`CreateTxnInput.TimePrecision` + `CreateBatch`）、
`internal/http/{router.go,dto.go,handlers_account.go,handlers_meta.go}`、`internal/pkg/{money/money.go,csvparse/detect.go,apierr/apierr.go}`、`internal/app/app.go`、`internal/config/config.go`（版本 1.2.0）。

## 新增端点（5 条）

| Method | Path | 说明 |
|---|---|---|
| POST | `/api/import/sheet/probe` | multipart `files[]`（同名重复，1..20 个）→ sheet 列表/表头候选/样本/映射建议 |
| POST | `/api/import/sheet/preview` | 映射 → 逐行解析、自动新增分类标签清单、时间分布样例（`rows` 最多 200，全量落盘） |
| POST | `/api/import/sheet/commit` | 幂等 + 200 行/事务分块入库 |
| POST | `/api/accounts/merge` | 合并账户（§2.5） |
| GET | `/api/import/batches?limit=20` | 批次审计列表（P2） |

`GET/PUT /api/settings` 与 `GET /api/meta` 增加 `time_precision`（`second|day`，非法值 400）。

## 关键实现口径

1. **迁移**：`Migrate()` = 全量 `schema.sql` → `ensureV12Columns()`（`PRAGMA table_info` 判断后 `ALTER`）→ **补过列则再执行一次 schema**（否则 `v_transactions_signed` 的 `t.*` 展开不到新列）→ `initMetaDefaults()` → `meta.schema_version=2`。`store.SchemaVersion` 常量与 `app.New` 的校验共用。
2. **账户合并默认自动做**（用户明确要求）：`app.New` 里 `AccountMergeService.AutoMergeOnUpgrade` 在 `Migrate/Seed` 之后、建路由之前执行：`meta.account_merge_v12_done=1` → 跳过；账户数 ≤1 → 直接置位；>1 → **先 `BackupService.Create` 备份**（失败则本次不合并、下次启动重试）→ 逐个账户记录合并前余额 → 单 `WriteTx` 搬 `counter_account_id` → 搬 `account_id`（含软删）→ 初始余额求和 → 删源账户 → 重绑 `default_account_id`/`last_used_account_id` → `frozen_write_flag=1` → 事务外 drop 源视图 / 建目标视图 / `ForceFull`。追溯日志追加到 `data/logs/v12-account-merge.log`（含每个账户合并前后余额）。
3. **`.xls` 不支持**：按扩展名 + OLE2 魔数判定 → 422 `UNSUPPORTED_SPREADSHEET`，消息「请用 Excel/WPS 另存为 .xlsx 或 .csv」。
4. **同日时间分布**：`DistributeOffset(i,n) = min((i*86400+43200)/n, 86399)`；行按 `(file_index, sheet_index, row_no)` 排序后**按 `local_date` 分组**取 `i`；`preview` 与 `commit` 调用同一函数；`commit` 只对选中行重算 → 与 `rows-preview.jsonl` 比对得出 `time_distribution_shift_warning`。
5. **金额**：`money.ParseAmountCents` 字符串定点（`863.1999999999999` → 86320），**`ParseYuan` 未改动**（v1.0 CSV 路径零回归）。
6. **去重**：`imp:` + `sha1(fileSHA1|sheetIndex|rowNo)[:20]` 命中 `ux_tx_external` → `skipped_dup`；软重复（同日/同额/同账户/同商户文本/同类型）只提示，`skip_soft_duplicates` 默认 false。
7. **`其他` ≠ `其它`**：`sheetparse.NormalizeName` 是名称处理的唯一入口（只 `TrimSpace`），有单测锁死；`EnsureCategoriesTx` 精确匹配 `(IFNULL(parent_id,''), name)`，冲突时回查复用。
8. **`CreateBatch`**：单 `WriteTx` 内逐行 `FindDuplicateTx` + `allocAndInsert` + `ReplaceTagsTx`，单行校验失败/重复计入跳过，DB 错误回滚整块；调用方每 200 行一块，块级失败重试 1 次后置 `partial=true`。

## 验证结果（真机）

```
go build ./...            ok（GOFLAGS=-mod=mod GOSUMDB=off GOPROXY=off）
go vet ./...              clean
go test ./... -count=1    ids/timeutil/money/csvparse/sheetparse/service/http 全绿
```
- 真实 `记帐.xlsx`（384KB / 4 张表）：probe **0.09s**、preview **0.12s**、commit **4.26s**（6746 行 / 34 个事务），全流程 4.47s（目标 <20s）。
- 6746 行全部入库、`SUM(amount_cents)=115423824` 分（=1,154,238.24 元），与独立脚本解 xlsx zip 定点求和**完全一致（差 0 分）**。
- `记帐` 表 `date_range = ["2018-10-10", "2026-11-13"]`（末行日期序列号 46339；`ARCHITECTURE-V12.md` §2.3 示例 JSON 已同步改正）。
- 自动创建 13 个分类（含 `其他` 与 `其它` 两个独立分类）；commit 实际新建 7 个（6 个命中种子复用）。
- 同日分布抽查：15 条/日 → 96 分钟步长、时分秒互不相同、严格递增、都落在当天；单条 → 12:00:00。
- 重复导入同一文件 → `dup_count=6746`、`created=0`；同一批次二次 commit → 409 `IMPORT_BATCH_COMMITTED`。
- `收入` 表 41 行 → 全部 `income`。
- v1.0 真实库（3 账户 / 13 笔）升级：schema_version 1→2、补 `time_precision` 列、建 `import_batches`、账户合并为 1 个「总账户」、**交易笔数 13 不变、总余额 1482300 分不变**、备份 zip 已生成、合并日志含逐账户余额；连续重启 3 次 0 错误、不重复建账户、不再产生备份。
- `scripts/smoke.py`：**146 PASS / 0 FAIL**（用「三账户形态」的库跑，见下方偏差 3）。
- 错误路径：`.xls` → 422；31MB → 422 `IMPORT_TOO_LARGE`；缺日期映射 → 400 `IMPORT_SHEET_INVALID`；无 token → 401。

## 与 v1.2 设计/契约的偏差

1. **首次启动自动合并**：§3.5 原本建议「有交易的老库不自动删账户，交给用户显式合并」，用户明确要求**自动做**，已按用户决策实现（含冻结区仍有交易的账户照常合并，但**先备份**并逐账户记日志）。`POST /api/accounts/merge` 保留，供以后再次合并。
2. **没有 `import_files` / `import_sheets` 表**：与 §3.1 一致（文件/表级元数据只在 `manifest.json`）。
3. **`scripts/smoke.py` 未改动**：其账户断言基于 v1.0 三账户种子（`acc_cmb0001` 等）。v1.2 单「总账户」后这些断言天然失效（非回归）。验证既有端点未回归时，用一个「三账户形态」的库（删除 `meta.account_merge_v12_done` 之外，把 3 个账户按 v1.0 形态写入并把迁移标记置 1 以跳过一次性合并）跑出 146/146。T09 建议把 smoke 的账户断言改为「取第一个账户 ID」。
4. **`probe` 响应额外字段**：`files[].sheets[].warning`（未识别表头提示）、`default_account_id`（请求带 `account_id` 时回显）；`merge` 响应额外 `account_details[]`（逐账户合并前余额，供追溯），均为附加字段，不影响既有契约字段。
5. **`preview` 的 `suggested_mapping`** 只包含命中项（未命中不出现），与 §3.4 的 `Partial<Record<...>>` 类型一致。
6. **`.xlsm`**：按 zip(xlsx) 处理（excelize 直接支持）；`.txt` 若像文本表格则按 CSV 解析。
7. **`header_row` 兜底**：`plan.header_row<=0` 时用 probe 探测值；探测值也无效时回落第 1 行；仍读不到表头 → 400 `IMPORT_SHEET_INVALID`。
8. **单表错误明细**上限 50 条（`errors[]`），避免 6746 行全脏时响应膨胀；全量错误计数在 `summary.error`。

## QA v1.2 复验修复（P1-1 / P2-1 / P2-2 / P2-5 / 观察项 2）

| # | 问题 | 修法 |
|---|---|---|
| P1-1 | 软重复被静默丢弃：`skip_soft_duplicates=false` 时内容重复仍计 `skipped_dup` | **根治在 CreateBatch**：不再用 `FindDuplicateTx` 的复合规则判重（其内容规则排在 `external_id` 之前，会先命中导致硬重复行被当软重复写入而撞唯一索引），改为**只查 `ExistsExternalIDTx`**：`external_id` 命中 → 硬重复跳过；内容重复 → **照常写入**。`skip_soft_duplicates` 仍由 import 层在进批前决定（`skipped_soft_dup`）。`skip_duplicates` 对批次导入退化为兼容参数（硬重复不可写入，唯一索引决定）。 |
| 计数守恒 | 可能静默丢行 | `Commit` 收尾断言 `created+skipped_dup+skipped_soft_dup+skipped_invalid == len(selected)`；不成立时 `slog.Error` 并回 `warnings[]`（`CommitResult.Warnings`，omitempty）。 |
| P2-1 | `merge` 未校验 confirm | `delete_sources=true && confirm!=true` → 400 `VALIDATION_ERROR`「删除源账户需确认」；`delete_sources=false` 不要求 confirm。 |
| P2-2 | `account_merge.go` 覆盖率 0% | 新增 `internal/service/account_merge_test.go`（9 个用例：正常合并/自定义目标名/空名回落/confirm 校验/源非法/自动迁移含备份+日志+幂等/seq 自愈），**文件语句覆盖率 74.7%**。 |
| P2-5 | `seq:global` 落后 → 新建交易 500 | 新增 `store.EnsureGlobalSeq()`：`seq:global <= max(created_seq)` 时抬到 `max+1`（只增不减）；启动任务 2.5/7 与**备份恢复后**各调用一次，修正时 `slog.Warn`。 |
| 观察项 2 | 源无时分秒却标 `second` | `sheetparse.ParseRow`：`!hasTime → 一律 day 并参与当日分布`；`hasTime && plan.time_precision==day → 同样降级 day 并分布`；只有「源有时间且未要求按天」才标 `second`。`plan.time_precision` 不再能把无时间的行升格为 `second`。 |

---

# v1.2 §9 延迟冻结（R6，T10–T12）

## 实现要点

| 项 | 落点 |
|---|---|
| 数据层 | `transactions` 新增 `freeze_confirmed`(默认 1) / `freeze_confirmed_at` / `import_batch_id`；`schema_version=3`；`migrate.go` 的 `v12Columns` 追加 3 条 ALTER（`PRAGMA table_info` 判存在），**2 条部分索引放在 `migrate.go` 的 `v9Indexes` 常量**里在补列之后幂等执行 |
| 写保护 | `service/freeze.go: IsWriteProtected(t, boundary)` 唯一入口；6 处全部收口（Create / Update 前置 / Update 改时间 / SoftDelete + 转账配对 / CreateBatch / dto.IsFrozen）。普通行（默认 1）行为与升级前逐字节一致 |
| 物化 | `materialize.go` 计算 `target = now - freezeDays` 后用 `MinUnconfirmedTime()` **钳制**：`clamp>0 && clamp<target → target=clamp`。**未给物化扫描加 `freeze_confirmed` 过滤**，`CurrentBalances`/`WindowBalances`/`verifySeam` 的 SQL 一行未改 |
| verify | 新增 `pending_rows`（信息量）/`materialization_gaps`（I2）/`stale_pending`（I3）；`checked_rows` 口径不变（只数已物化行）。`ok` 受 gaps/stale 影响 |
| 确认端点 | `POST /api/transactions/freeze-confirm`（端点 75）：`transaction_ids`(≤5000) / `batch_id` / `all=true`(需 `confirm:true`)，优先级按此顺序；tx1 确认+置 flag、tx2 物化（失败不回滚确认，返回 `materialized:false`+`materialize_error`）；混入不存在 id → `skipped_missing` |
| 导入 | `POST /api/import/sheet/commit` 新增 `freeze_mode`（缺省/非法值 = `defer`）；每行注入 `import_batch_id`；`defer` 时早于边界的行 `freeze_confirmed=0`。收尾若 `minDeferredTime < last_frozen_at` → 置 `frozen_write_flag=1` **并当场 `ForceFull()`**，响应回 `rematerialized`/`rematerialize_rows`。端点 54/55（微信/支付宝 CSV）**零改动** |

## 与设计的偏差（3 处，均已在代码注释说明）

1. **两条部分索引不在 `schema.sql`**：`schema.sql` 先于 `ensureV12Columns()` 执行，老库（无 `freeze_confirmed` 列）执行到 `CREATE INDEX ... WHERE freeze_confirmed=0` 会直接 `no such column` 报错。统一放到 `migrate.go: v9Indexes`，补列之后每次 Migrate 幂等执行（新库同样生效）。
2. **Update 把待确认行改到 `last_frozen_at` 之前时补置 `frozen_write_flag=1`**：§9.3 表 #3 允许「待确认行改到任何历史时间」，但若改到已物化区之前会违反 I3（该行金额会暂时消失）。因此该分支额外置 flag，让下一次查询 `ForceFull` 把边界回退到该行（`ForceFull` 会先 `last_frozen_at=0`，不受"边界不后退"限制）。
3. **`CreateBatch` 的 `skipDuplicates` 参数**（P1 修复遗留）对批次导入仍是兼容参数：`external_id` 一律跳过、内容重复一律写入。

## 真机验收（`记帐.xlsx` / `记帐` 6746 行，`freeze_mode` 缺省=defer）

| 项 | 数字 |
|---|---|
| commit | `created=6746`、`rematerialized=true`、耗时 3.5s |
| 待确认行数 | **6709** == 早于冻结边界的行数 6709（冻结区 37 行属动态区） |
| `last_frozen_at` | 钳制到最早待确认行 **2018-10-10 12:00:00** |
| 余额 | API **-115,423,824** == 独立 SQL 全表求和 **-115,423,824**（差 0） |
| 2018 老行 | `PUT` 改金额 **200**、`DELETE` **200**（需求核心） |
| 确认（batch_id） | `confirmed=6709`、`pending_remaining=0`、`materialized=true`、`materialize_rows=6709`、**67ms**（端点总耗时 100ms） |
| 确认后同一笔 `PUT` | **409 FROZEN**（保护恢复） |
| 确认前后余额 | **-115,423,924 → -115,423,924**（差 0，钳制方案不重不漏） |
| verify | `ok=true`、`checked_rows=6709`、`pending_rows=0`、`gaps=0`、`stale_pending=0` |
| immediate（收入表 41 行） | `created=41`、`pending=0`、`confirmed=41`、`last_frozen_at` 不变、`rematerialized=false` |

## 性能（6746 行全在动态区 / 6709 行待确认，Go 进程内实测）

| 查询 | 耗时 |
|---|---|
| `MinUnconfirmedTime`（6709 待确认） | **0.93 ms** |
| `CurrentBalances`（动态区 6746 行） | **1.2 ms** |
| `WindowBalances` | **29 ms** |
| `BalanceService.Current()`（含 EnsureFresh） | **1.2 ms** |
| `Verify` | **12 ms** |
| HTTP 导入后首次 `GET /api/accounts?with_balance=true` | **4 ms** |
| 确认端点（6709 行确认 + 全量重算） | **100 ms** |

## 迁移幂等

连续重启 2 次：`schema_version` 恒为 `3`、3 列存在、2 条部分索引存在、6787 行/Pending 计数不变、日志无错误。`internal/store/migrate_test.go` 另用「DROP COLUMN 模拟老库 → 再 Migrate」验证补列路径与 `v_transactions_signed` 视图重建。

## 前端联调补齐（3 项）

| # | 问题 | 修法 |
|---|---|---|
| 1 | `GET /api/transactions` 不支持 `freeze_pending` 过滤 | `TxQuery.FreezePending *bool`（nil 不过滤 / true 只看 `freeze_confirmed=0` / false 只看 `=1`），落 `BuildTxWhere`；handler 接受 `1\|true\|yes` 与 `0\|false\|no`，其它值 400。**注意 summary 与该过滤同源**（同一 where），因此「待确认 N」页签的合计也是准的 |
| 2 | `commit` 响应缺 `pending_frozen` | `CommitResult.PendingFrozen`：本次新建行里 `freeze_confirmed=0` 的笔数（与 `frozen_created` 并列，后者是「落在冻结区」的统计口径） |
| 3 | I3（待确认行不得有物化值） | 两道保险：① `TxRepo.InsertTx` 对 `FreezeConfirmed==0` 的行显式清空 `account_balance_after/total_balance_after/materialized_at`；② `materializeLocked` 每次进入写事务先 `UPDATE ... SET 三列=NULL WHERE freeze_confirmed=0 AND account_balance_after IS NOT NULL`（I3 自愈，走 `ix_tx_unconfirmed`，~1ms），**先于**"无需推进即返回"的分支，所以任何一次物化都会清理。另补 §9.9 的 `scripts/unfreeze-imported.sql`（解冻时同步置空三列 + 置 `frozen_write_flag=1`），避免脚本自身制造 stale 行 |

> 关于前端看到的 `-1 / -39 / -114`：那是 **DTO 从窗口余额填的值**（`is_materialized=false` → 用 `WindowBalances` 覆盖），不是库里的 `account_balance_after`。库内实测 `freeze_confirmed=0 AND account_balance_after IS NOT NULL` = **0**。按 §9.4/§9.8，待确认行的余额无历史意义，前端按 `freeze_pending` 显示 `—`。

### 真机复验（defer 导入 6746 行，未确认状态）

```
commit: created=6746 pending_frozen=6709 frozen_created=0 rematerialized=true
I3:     pending=6709  stale_accAf=0
verify(未确认): ok=true pending_rows=6709 stale_pending=0 gaps=0
freeze_pending=1 → total=6709 ；freeze_pending=0 → total=37 ；freeze_pending=x → 400
confirm(batch): confirmed=6709 materialized=true 耗时 150ms
verify(确认后): ok=true pending=0 stale=0 gaps=0 ；freeze_pending=1 → 0
smoke.py: 146 PASS / 0 FAIL
```

## QA 第二轮缺陷修复（P1-2 / P2-7）

| # | 缺陷 | 根因 | 修法 |
|---|---|---|---|
| P1-2 | `Restore` 恢复「待确认行」时余额静默少算（违反 I3 时间侧） | 待确认行软删期间物化边界越过它（同批其它行被确认 / 主动 rematerialize 都会推进）→ 恢复后该行 `< last_frozen_at` 且 `accAf IS NULL`，**既不进快照也不进动态窗口**，金额凭空消失；且 `Materialize` 有「边界不后退」保护，普通补偿永远修不回来 | `Restore` 事务前取 `LastFrozenAt()`，对**每一条**被恢复行（含转账配对）判断 `FreezeConfirmed==0 && time < last`；事务提交后调 `forcePendingBoundaryRefresh()`：置 `frozen_write_flag=1` **并立即 `balance.Current()` 触发 `ForceFull`**（边界回退到该行），不把错误余额留给下次查询。`Update` 改时间分支复用同一 helper。`Purge` 无此问题 |
| P1-2 加固 | `Verify` 看不见这类缺陷（变体场景 `ok=true`） | 只有断言 A（confirmed & `<last` & accAf NULL）与 B（pending & accAf NOT NULL） | 新增**断言 C**：`freeze_confirmed=0 AND deleted_at IS NULL AND transaction_time < last_frozen_at` 的计数 → 新字段 `pending_before_boundary`，`>0` 则 `ok=false` |
| P2-7 | 调整分录被写成 `freeze_confirmed=0`（「待确认冻结」） | `InsertTx` 用 `if t.FreezeConfirmed != 0` 判断「是否指定」，而 Go 零值 0 同时代表「未指定」与「显式待确认」→ 直接构造 `store.Transaction` 的路径（`adjust.go:baseAdjustment`，6 处调用）全落 0 | 消除零值歧义：`store.Transaction` 增加不落库的 `FreezeSpecified bool`（`gorm:"-"`）；`InsertTx` 改为 `if !t.FreezeSpecified { FreezeConfirmed = 1 }`；`Create`/`CreateBatch` 显式置 `FreezeSpecified=true`（它们有 `FreezeConfirmed *bool` 的明确意图）；`baseAdjustment` 显式写 `FreezeConfirmed: 1, FreezeSpecified: true` 自证语义 |

### 真机复验（QA 脚本，`ledger-qa2.exe` + 8775）

- `r6_min_repro2.py`（P1 原复现路径 → 已转为**修复态回归用例**）：恢复后「API 余额 = 独立 SQL 全表求和，差 **0**」（修复前 −44,444 vs −77,777），该行 `freeze_pending=true / is_frozen=false`，`last_frozen_at` 回退到 2020-03-03，`verify.ok=true / pending_before_boundary=0` → **5 PASS / 0 FAIL**。
- `r8_p12_verify.py`：**29 PASS / 0 FAIL**（干净库）。其 `early` 计数原先未按批次过滤（同库先跑 P1-2 段会多算 2 行），QA 已改为按 `import_batch_id` 过滤并保留一条「全库待确认」断言；本批 `pending == early == 6709`。
- `r9_adj.py`：**8 PASS / 0 FAIL**（adjust / reverse 全部 `freeze_confirmed=1`、`freeze_pending=false`、`pending_rows` 不虚增、`verify.ok=true`）。

> **修复后复验汇总（QA 干净库）**：`r8_p12_verify` 29/29、`r6_min_repro2` 5/5、`r6_min_repro` 6/6、`r9_adj` 8/8、`r6_4b_edges` 28/28、`r6_4f_restore` 10/10、`scripts/smoke.py` 146/146；最终 P0/P1/P2 = **0/0/0**。

## 写入路径审计（§9.3 I3：待确认行不得物化、已确认行必须物化）

判据：这条写路径是否可能让某行的「是否落在冻结区 / 是否有物化值 / 归属账户」发生变化？
若有，就必须置 `frozen_write_flag=1`（→ 下次查询或当场 `ForceFull`）或当场重算。

| # | 路径 | 改动什么 | 可能落进冻结区/物化区？ | 置 flag / 重算 | 备注 |
|---|---|---|---|---|---|
| 1 | `Create`（支出/收入） | 新增行 | 是（`frozen` = 已确认且 `time < boundary`） | ✅ 置 flag（`meta.SetTx`） | 另加防御：`!confirmed && time < last_frozen_at` → 也置 flag |
| 2 | `Create`（转账双边） | 新增 2 行 | 同上 | ✅ 同上（同一次事务只需一次） | 双边同时间，`frozen` 判定一致 |
| 3 | `Update` 改时间 | `transaction_time` | 是 | ✅ 已确认行 `< boundary` → **409**；待确认行改到 `< last_frozen_at` → `forcePendingBoundaryRefresh()`（置 flag + 立即 ForceFull） | `last <= boundary`，故 `<last` 一定 `<boundary` |
| 4 | `Update` 改金额 | `amount_cents` | 否 | ⛔ 不需要 | 物化行先被 409 拦住；动态行由窗口求和自动生效 |
| 5 | `Update` 改账户 | `account_id` | 否 | ⛔ 不需要 | 物化行 409；动态行由 `CurrentBalances` 按账户实时求和 |
| 6 | `Update` 改分类/商户/备注/标签/明细 | 非金额字段 | 否 | ⛔ 不需要 | 不影响余额口径 |
| 7 | `SoftDelete`（含转账配对） | `deleted_at` | 是（行退出余额口径） | ⛔ 不需要 | 冻结区已确认行先被 409；动态行退出后由窗口求和自动生效 |
| 8 | **`Restore`（含转账配对）** | `deleted_at=NULL` | **是 —— 原来会静默吞钱** | ✅ **`forcePendingBoundaryRefresh()`（置 flag + 立即 `ForceFull`）** | **P1-2 修复点**：待确认行软删期间边界越过后，恢复时该行 `< last_frozen_at` 且无物化值 → 既不进快照也不进窗口 |
| 9 | `Purge` | 物理删除 | 否 | ⛔ 不需要 | 只能删「软删满 180 天」的行，其 `time < deleted_at < now-180d`，且物化扫描本就排除软删行 → 删与不删都不影响快照 |
| 10 | `CreateBatch`（导入） | 批量新增 | 是 | ✅ 整批**只写一次** flag；commit 收尾若 `minDeferred < last_frozen_at` 则当场 `ForceFull` | 防 I3 时间侧：`!rowConfirmed && time < last_frozen_at` 也计入 `frozenHit` |
| 11 | `Adjust` / `Reverse`（含转账调整） | 新增补偿行（时间 = now） | 否（新行必在动态区） | ⛔ 不需要 | 源行金额不变（只追加补偿行）；调整行显式 `FreezeConfirmed=1`（P2-7） |
| 12 | `accounts/merge` | 批量改 `account_id`/`counter_account_id` | 是（含已物化行） | ✅ 置 flag + 事务外 `ForceFull` | 合并后视图重建 + 全量重算 |
| 13 | `freeze-confirm`（端点 75） | 批量改 `freeze_confirmed` | 是（解除钳制 → 边界前进） | ✅ 置 flag + tx2 `ForceFull`（失败不回滚确认） | 幂等：无可改行时不重算 |
| 14 | `backup/restore` | 整库替换 | 是 | ✅ `Migrate` + `EnsureGlobalSeq` + `ForceFull` | 恢复后全量重算 |
| 15 | `maintenance/rematerialize` | 强制重算 | — | ✅ 自身即重算 | `full=true` → `ForceFull`；`false` → `Materialize`（受「边界不后退」约束，故前面各路径必须置 flag） |

**本轮审计结论：漏 2 条**
1. **`Restore`（真漏，P1-2）**：恢复待确认行不置 flag → 余额静默少算（QA 实测差 333.33 元，且 `rematerialize{full:false}` 修不回来）。已修为「提交后 `forcePendingBoundaryRefresh()`」。
2. **`Create` / `CreateBatch` 的「待确认 + 早于 `last_frozen_at`」（潜在漏）**：当前无 HTTP 调用方这么传（导入走 commit 会重算），但输入里已有 `FreezeConfirmed` 字段，一旦开放就会吞钱。已加防御性置 flag + `TestEdge13` 覆盖。

其余 13 条均「要么已置 flag、要么本就不需要」（详见上表备注列）。

**P2（verify 时间侧断言）**：`Verify` 已补断言 C —— `freeze_confirmed=0 AND deleted_at IS NULL AND transaction_time < last_frozen_at` 的计数写入 `pending_before_boundary`，`>0` 则 `ok=false`（与断言 A/B 并列，覆盖「钳制被破坏」这一类）。

## 新增写入路径自查清单（§9 遗留提醒）

以后新增任何「会写 `transactions`」的路径，对照两条：

1. **`FreezeSpecified`**：直接构造 `store.Transaction` 时，若该行可能是「待确认冻结」必须显式
   `FreezeConfirmed: 0, FreezeSpecified: true`；否则 `InsertTx` 会按历史语义写成 `1`（安全，但
   「待确认」会静默失效）。目前显式置位的只有 `Create` / `CreateBatch` / `baseAdjustment`。
2. **是否置 `frozen_write_flag`**：只要该路径能让某行「跨过 `last_frozen_at`」（新增/恢复/改时间/
   改账户/改归属），就必须置 flag（或调 `forcePendingBoundaryRefresh()`）；否则金额会被静默少算或多算。
   完整对照见上面的 15 条审计表。

**已知限制（非缺陷）**：`forcePendingBoundaryRefresh()` 里 `balance.Current()` 失败时只记 `slog.Error`
并让本次请求仍返回 200（`Restore` 接口无 warnings 通道；`freeze-confirm` 有 `materialize_error`）。
此时余额可能短暂偏小，但 `Verify.pending_before_boundary` 会立刻报 `ok=false`，不会静默；
`frozen_write_flag` 仍为 `1`，下次查询/启动继续补偿。若将来给 `Restore` 加响应体，可顺手降级为 warnings。
