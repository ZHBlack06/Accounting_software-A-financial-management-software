# 前端交付说明（T09 / T10 / T11 / T12 / T13 / T14 / T15）

## 范围
- 目录：`web/`（Vue 3.5 + TS 5.6 + Vite 5 + Element Plus 2.8 全量 + ECharts 5.5 + Pinia 2 + vue-router 4 + axios；CSS 变量 + SCSS，无 Tailwind）。
- 本批交付：骨架、Dashboard、交易明细、记账表单、设置页 8 tab、报表中心 4 报表、导入中心 CSV+OCR。

## 页面与路由
| 路由 | 页面 | 内容 |
|---|---|---|
| `/login` | LoginView | token 登录（localStorage），401 自动跳回 |
| `/` | DashboardView | 摘要卡 2×2 + 趋势（折线↔柱状、日/周/月/年、点击下钻）+ 分类占比（环形↔饼、中心总额）+ 账户余额多折线 + 分类排行 + 高额消费 + 最近交易 |
| `/transactions` | TransactionsView | 12 筛选器 + 标签条 + 分页 20/50/100/200 + summary；详情抽屉；冻结橙条→调整闭环（409 FROZEN 自动切 AdjustDialog）；转账只读；回收站 |
| `/reports` | ReportsView | 4 tab 保活：分类（一级/二级、占比、环比/同比、排除调整项、行点击钻取 `/transactions` 带 query）；账户（收入/支出/转入/转出/余额 + 总余额一致性校验）；标签（any/all 交叉、去重合计）；周期对比（月/季/年 + base + 三列差异 + 分组柱状图，排除调整项默认开） |
| `/import` | ImportView | CSV 两阶段（拖拽→预览→可编辑→提交）+ OCR 上传轮询 |
| `/settings` | SettingsView | 8 tab：账户/分类/标签/商户/账本/规则/系统/备份，均 CRUD |

## 关键实现点
- **设置（T10）**：账户 display_name 预览「名称（后四位）」、后四位 4 位校验、初始余额元→分、↑↓排序调 `POST /accounts/reorder`、409 ACCOUNT_IN_USE 引导归档、冻结账户改初始余额显示 409 FROZEN 文案；分类两级树（二级限同 transaction_type 一级）+ PALETTE 10 色；商户按 use_count desc + ≥10 次候选分组一键确认；账本规则编辑器 5 类 target_type 多选 + or/all，保存后刷新 transaction_count；系统设置冻结天数 10–365 步长 5（含生效提示）+ 模板增删改排序；备份创建/列表/RESTORE 二次确认 + 全屏 loading；地区远程搜索 300ms 防抖、值为 code（在记账表单内）。
- **导入（T15）**：CVS dup 行黄底 + tooltip（原因/matched_transaction_id）、frozen 行橙底 + 顶部警示条、skipped 行可展开原因；全选/反选/仅选非重复/仅选非冻结；确认框「创建 N 笔，跳过重复 M 笔，含冻结 K 笔」；含冻结未勾选 allow_frozen → 前端拦截；结果显示 created/skipped_dup/skipped_invalid/frozen_created + 查看新交易。OCR 拖拽/粘贴(⌘V)/多选、1.5s 轮询 120s 超时、need_review 黄框、账户/分类选择器、确认后预填 TransactionForm、503 降级跳快捷记账、图片走 `GET /api/screenshots/:id/file`（认证 blob → objectURL）。
- 时间范围切换 → stats store `fetchAll` 并发 6 块，请求序号丢弃乱序；列表 500ms 防抖、同序号机制。
- BaseChart：init + watch(option) + ResizeObserver + dispose；深色经 `--chart-text/--chart-split` 重设 option。
- 单测：presets（14）、money（11），`npx vitest run --no-file-parallelism` 25 全绿。

## Mock 开关
`MOCK = import.meta.env.VITE_MOCK === 'true'`（默认 false）。开发期 `VITE_MOCK=true npm run dev` 由 `src/api/mock.ts` 提供假数据；CSV/OCR 端点故意返回 422/503 以验证降级路径。

## 构建
`npm install` ✓；`npx vue-tsc --noEmit` ✓ 0 错误；`npm run build` ✓ → `web/dist/`。gzip：首屏 index 40.6KB；Dashboard 路由懒加载 element-plus 340KB + echarts 343KB。

## 已知偏差 / 未完成
1. 「规则」tab 复用 LedgerSettings（规则编辑器在账本行内打开），未做独立列表页。
2. 商户「候选词条」无后端候选端点，实现为 use_count≥10 的提示分组 + 一键确认（不新建记录）。
3. OCR「一键全部确认」未做合并提交：逐条确认（字段冲突需逐条判断），已提供批量上传与逐条状态。
4. Dashboard 详情抽屉编辑/调整引导至明细页（单一编辑入口）。
5. 系统设置未提供「Token 更换」（契约无此端点）。
6. 后端未联调，全部按 ARCHITECTURE §5 契约编码；`web/.tmp` 空目录为沙箱残留（无害）。

---

# v1.2 前端增量（T06 / T07 / T08 + R5 账户合并入口）

## 范围
按 `docs/ARCHITECTURE-V12.md` §2 契约与 §5 交互实现；后端未就绪时按契约编码（`VITE_MOCK=true` 可用假数据走完 5 步）。

## 新增 / 修改文件
| 文件 | 类型 | 说明 |
|---|---|---|
| `src/api/imports.ts` | 改 | 追加 `probeSheets(files, accountId)` / `previewSheets(payload)` / `commitSheets(payload)`；CSV 两函数保持原样 |
| `src/api/types.ts` | 改 | `TimePrecision`；`MetaDTO/SettingsDTO.time_precision`；`TransactionDTO.time_precision`；sheet 导入全套 DTO（`SheetProbeDTO`/`SheetPreviewReq`/`SheetPreviewDTO`/`SheetCommitReq`/`SheetCommitResult`）；`AccountMergeReq/Result` |
| `src/api/accounts.ts` | 改 | `mergeAccounts(payload)` |
| `src/stores/meta.ts` | 新增 | `timePrecision` / `precision` / `isDayPrecision`；`applyDto` / `load` / `setTimePrecision`（PUT /settings）/ `setLocalPrecision` |
| `src/utils/datetime.ts` | 改 | `fmtTxnTime` / `fmtFullTxnTime` / `fmtBucketLabel` / `applyPrecision` |
| `src/utils/sheetMapping.ts` | 新增 | `MAPPING_FIELDS`、`checkSheetFile`（`.xls` 拦截）、`validateMapping`、`applyMappingByColumnName`、`pickPrecision` |
| `src/components/import/sheet/types.ts` | 新增 | 向导状态 `WizardState`、`buildCandidates` / `syncConfigs` / `buildPreviewReq` / `buildCommitReq` / 指纹 |
| `.../sheet/SheetImportWizard.vue` | 新增 | 5 步壳 + el-steps + 上一步/下一步 + 探测/预览/提交编排 + 失败回退保留状态 |
| `.../sheet/FileUploadStep.vue` | 新增 | 步 1 拖拽 + 多选（≤20 个 / ≤30MB），`.xls` 立即拦截 |
| `.../sheet/SheetPicker.vue` | 新增 | 步 2 按文件分组、表头行选择、样本预览、汇总表默认不勾选 + 原因 |
| `.../sheet/MappingStep.vue` + `ColumnMapper.vue` | 新增 | 步 3 折叠卡片、必填红点、建议标、占用列置灰、自动识别、应用到其他表 |
| `.../sheet/PreviewStep.vue` + `SheetPreviewTable.vue` | 新增 | 步 4 汇总条 / 表级 Tab / 行表（错误红·软重复黄·冻结橙）/ 自动新增分类标签 / 时间分布样例 / 提交选项 |
| `.../sheet/ResultStep.vue` | 新增 | 步 5 created / skipped_* / frozen_created + 查看新交易（带 `start_date`/`end_date`） |
| `src/views/ImportView.vue` | 改 | 新增「Excel / CSV 账本导入」页签（原 CSV / OCR 面板未删）；OCR 预填弹窗加宽 |
| `src/utils/datetime.spec.ts` | 新增 | 12 例（`second`/`day` 输出、跨年、桶文案、`applyPrecision`） |
| `src/utils/sheetMapping.spec.ts` | 新增 | 15 例（缺日期/金额、重复映射、`.xls` 拦截文案、批量套用） |
| `src/components/entry/TransactionForm.vue` | 改 | day 模式日期选择器 + `:deep(.el-form-item__content){min-width:0}` |
| `src/components/transaction/TransactionItemEditor.vue` | 改 | 列宽收敛 + 容器 `overflow-x:auto`（T08） |
| `src/components/transaction/TransactionTable.vue` / `TransactionDetailDrawer.vue` / `RecycleBinDrawer.vue` / `DashboardView.vue` / `charts/{TrendChart,AccountBalanceChart,CategoryRankingList,HighAmountList}.vue` | 改 | 按 `meta.precision` 格式化；导入行（`time_precision=day`）补「时间由系统按当日顺序分布」tooltip |
| `src/components/settings/SystemSettings.vue` | 改 | 新增「记录时间精度」radio（写 `settings.time_precision`） |
| `src/components/settings/AccountSettings.vue` | 改 | 顶部绿色提示卡 + 「合并为总账户」（多选/目标名/删除源账户 + 输入 `MERGE` 二次确认） |
| `src/layouts/AppLayout.vue` | 改 | meta 落 `metaStore`；完整表单弹窗 640px → `min(880px, 94vw)` + `dlg--wide` |
| `src/components/transaction/TransactionEditDialog.vue` | 改 | 同上加宽 + `dlg--wide` |
| `src/styles/index.scss` | 改 | `.dlg--wide .el-dialog__body{padding:16px 20px}`；全局 `.el-form-item__content{min-width:0}` 兜底 |
| `src/api/mock.ts` | 改 | 补 `/import/sheet/{probe,preview,commit}`、`/accounts/merge` 假数据，`meta.time_precision='second'` |

## T08 物品明细溢出：宽度计算（1280×860 视口）
**修复前**：弹窗 `640px`，EP 默认 body 内边距 `30px 20px`，`label-width:88px`
→ 表格可用宽度 = 640 − 40 − 88 = **512px**；列最小宽度合计 120+120+120+90+100+90 = **640px** → 溢出 **128px**。
且 `.el-form-item__content` 是 flex 子项、默认 `min-width:auto`，不会收缩，于是整表把弹窗顶开（白底溢出，与截图一致）。

**修复后**：弹窗 `min(880px, 94vw)` → 1280 视口下取 **880px**；`.dlg--wide` body 内边距 `16px 20px`
→ 表格可用宽度 = 880 − 40 − 88 = **752px**；列最小宽度合计 140+110+110+80+120+96 = **656px**
→ **752 ≥ 656，6 列全部可见（余量 96px），无横向滚动**。
窄屏（如 800px 视口）：`94vw = 800 × 0.94 = 752px` → 表格可用宽度 = 752 − 40（body 左右内边距）− 88（label）= **624px**；
624px < 656px → 溢出 = 656 − 624 = **32px**，表现为表格**内部**横向滚动条（`.item-editor` 的 `overflow-x:auto` + 表单内 `:deep(.el-form-item__content){min-width:0}` 保证收缩），弹窗本身不超出视口；
这条链路由 `.item-editor{min-width:0;max-width:100%;overflow-x:auto}` + 表单内 `:deep(.el-form-item__content){min-width:0}` 保证。

## T07 一处与 §5.4 的差异
§5.4 写「day → 列表显示 `MM-DD`（跨年 `YYYY-MM-DD`）」；本批按主理人口径统一为 **`day` 模式一律 `YYYY-MM-DD`，不出现时间分隔符与 `00:00:00`**（列表 / 详情 / 报表 tooltip 一致），避免同一页出现两种日期粒度。其余（行级 `time_precision` tooltip、表单只选到日且保留原有时分）与 §5.4 一致。

## 验证
- `npx vue-tsc --noEmit` → 0 错误。
- `npx vitest run --no-file-parallelism` → 4 文件 / **52 例全绿**（本轮新增 27 例）。
  （本机沙箱限制 `%TEMP%` 写入，`vitest run` 并行模式会因 SSR 缓存 EPERM 报错，故沿用 v1.1 的 `--no-file-parallelism` 口径。）
- `npm run build` → 成功；首屏 `index` gzip **40.62 kB**，`ImportView` 路由 gzip 20.42 kB（新增向导），element-plus 340.26 kB / echarts 343.41 kB 未变。
- `npm run dev`（5173）：`/`、`/import`、`/transactions`、`/settings` 与各 SFC 模块均 **200**。
  注：本机沙箱下 vite 只监听 `::1`，`curl 127.0.0.1` 拒绝、`curl localhost` 正常，非配置问题。

## 待联调（依赖后端）
1. `probe/preview/commit` 三端点的真实响应（字段口径已按 §2 编码；`sheet_index` 定位、`rows_truncated`、`transaction_ids_omitted`）。
2. `PUT /settings {time_precision}` 与 `GET /meta.time_precision` 回填。
3. `POST /accounts/merge` 结果文案（`merged_accounts`/`moved_transactions`）与合并后账户列表刷新。
4. 明细页「查看新交易」目前传 `start_date`/`end_date`：需要后端确认导入行的 `local_date` 区间与预览 `date_range` 一致。
