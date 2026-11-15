<template>
  <div class="transactions-view">
    <PageHeader title="交易明细">
      <template #extra>
        <el-button size="small" @click="recycleOpen = true">回收站</el-button>
      </template>
    </PageHeader>

    <TransactionFilters :value="ui.filters" @change="onFiltersChange" @reset="ui.resetFilters()" />

    <FilterTagBar :tags="activeTags" @remove="removeFilter" @clear="ui.resetFilters()" />

    <div class="card txn-list">
      <div class="txn-list__toolbar">
        <TimeRangePicker
          :preset="ui.timePreset"
          :custom-range="ui.customRange"
          @update:preset="ui.setPreset($event)"
          @update:custom-range="(r) => (ui.customRange = r)"
          @change="scheduleRefresh"
        />
        <div class="txn-list__right">
          <!-- v1.2 R6：待确认冻结入口（蓝灰虚线，与已冻结的橙色区分） -->
          <el-tooltip
            v-if="pendingCount > 0"
            content="导入的历史记录尚未确认冻结，核对无误后可批量确认"
            placement="top"
          >
            <el-button
              size="small"
              class="btn-freeze-pending"
              :type="pendingOnly ? 'primary' : 'default'"
              @click="togglePending"
            >
              {{ pendingFilterButtonText(pendingCount) }}
            </el-button>
          </el-tooltip>
          <el-select :model-value="txnStore.sort" size="small" style="width: 130px" @update:model-value="txnStore.setSort($event as never)">
            <el-option value="time_desc" label="时间 ↓" />
            <el-option value="time_asc" label="时间 ↑" />
            <el-option value="amount_desc" label="金额 ↓" />
            <el-option value="amount_asc" label="金额 ↑" />
            <el-option value="created_desc" label="创建 ↓" />
          </el-select>
        </div>
      </div>

      <el-alert
        v-if="txnStore.error"
        type="error"
        :title="`加载失败：${txnStore.error}`"
        show-icon
        :closable="false"
        style="margin-bottom: 8px"
      >
        <el-button size="small" @click="txnStore.fetchList()">重试</el-button>
      </el-alert>

      <div class="txn-list__table">
        <TransactionTable
          :items="txnStore.items"
          :loading="txnStore.loading"
          @detail="openDetail"
          @edit="openEdit"
          @adjust="openAdjust"
          @remove="onRemove"
        />
      </div>

      <!-- summary 行：当前筛选全量聚合，不受分页影响 -->
      <div v-if="txnStore.summary" class="txn-list__summary">
        <span>收入 <MoneyText :cents="txnStore.summary.income_cents" type="income" :bold="true" /></span>
        <span>支出 <MoneyText :cents="txnStore.summary.expense_cents" type="expense" :bold="true" /></span>
        <span>结余 <MoneyText :cents="txnStore.summary.net_cents" type="auto" :bold="true" /></span>
        <span v-if="txnStore.summary.adjustment_net_cents !== 0">
          调整净额 <MoneyText :cents="txnStore.summary.adjustment_net_cents" :is-adjustment="true" />
        </span>
        <span class="text-tertiary">共 {{ txnStore.summary.count }} 笔</span>
      </div>

      <el-pagination
        :current-page="txnStore.page"
        :page-size="txnStore.pageSize"
        :total="txnStore.total"
        :page-sizes="[20, 50, 100, 200]"
        layout="total, sizes, prev, pager, next, jumper"
        background
        @current-change="txnStore.setPage"
        @size-change="txnStore.setPageSize"
      />
    </div>

    <TransactionDetailDrawer
      :open="txnStore.drawerOpen"
      :loading="txnStore.detailLoading"
      :detail="txnStore.detail"
      :confirming="freezeConfirming"
      @update:open="(v) => (v ? (txnStore.drawerOpen = true) : txnStore.closeDrawer())"
      @edit="openEdit"
      @adjust="openAdjust"
      @jump="jumpTo"
      @confirm-freeze="onConfirmFreeze"
    />

    <TransactionEditDialog
      :visible="txnStore.dialogOpen"
      :transaction="txnStore.editing"
      @update:visible="(v) => (v ? (txnStore.dialogOpen = true) : txnStore.closeEdit())"
      @saved="txnStore.fetchList()"
      @frozen-conflict="onEditFrozen"
    />

    <AdjustDialog
      :visible="adjustOpen"
      :transaction="adjustTarget"
      @update:visible="adjustOpen = $event"
      @adjusted="onAdjusted"
    />

    <RecycleBinDrawer v-model:open="recycleOpen" @changed="txnStore.fetchList()" />
  </div>
</template>

<script setup lang="ts">
/**
 * 交易明细页（T11）：12 个筛选器 + 标签条 + 分页表格 + summary 行 + 详情/编辑/调整/回收站。
 * 筛选变化 → 页码重置为 1；500ms 防抖；乱序响应由 store 序号丢弃。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useUiStore, type UiFilters } from '@/stores/ui'
import { useTransactionStore } from '@/stores/transaction'
import { useMasterStore } from '@/stores/master'
import { errorText, type ApiError } from '@/api/client'
import { confirmFreeze } from '@/api/transactions'
import { listTransactions } from '@/api/transactions'
import { formatCents } from '@/utils/money'
import { pendingFilterButtonText } from '@/utils/freeze'
import type { TransactionDTO, TransactionQuery } from '@/api/types'
import PageHeader from '@/components/common/PageHeader.vue'
import FilterTagBar, { type FilterTag } from '@/components/common/FilterTagBar.vue'
import TimeRangePicker from '@/components/common/TimeRangePicker.vue'
import MoneyText from '@/components/common/MoneyText.vue'
import TransactionFilters from '@/components/transaction/TransactionFilters.vue'
import TransactionTable from '@/components/transaction/TransactionTable.vue'
import TransactionDetailDrawer from '@/components/transaction/TransactionDetailDrawer.vue'
import TransactionEditDialog from '@/components/transaction/TransactionEditDialog.vue'
import AdjustDialog from '@/components/transaction/AdjustDialog.vue'
import RecycleBinDrawer from '@/components/transaction/RecycleBinDrawer.vue'

const ui = useUiStore()
const txnStore = useTransactionStore()
const master = useMasterStore()
const route = useRoute()
const router = useRouter()

const adjustOpen = ref(false)
const adjustTarget = ref<TransactionDTO | null>(null)
const recycleOpen = ref(false)
/** v1.2 R6：待确认冻结筛选与计数。 */
const pendingOnly = ref(false)
const pendingCount = ref(0)
const freezeConfirming = ref(false)

let debounceTimer: ReturnType<typeof setTimeout> | null = null

/** 合成最终查询（ui.filters + 时间范围 → API 参数）。 */
function buildQuery(): TransactionQuery {
  const f = ui.filters
  return {
    start_time: ui.range.start,
    end_time: ui.range.end,
    account_id: f.accountIds.length ? f.accountIds : undefined,
    category_id: f.categoryId ?? undefined,
    sub_category_id: f.subCategoryId ?? undefined,
    tag_ids: f.tagIds.length ? f.tagIds : undefined,
    merchant_id: f.merchantId ?? undefined,
    type: f.type.length ? f.type : undefined,
    ledger_id: f.ledgerId ?? undefined,
    amount_min_cents: f.amountMin ?? undefined,
    amount_max_cents: f.amountMax ?? undefined,
    include_adjustments: f.includeAdjustments,
    keyword: f.keyword || undefined,
    freeze_pending: pendingOnly.value ? true : undefined,
  }
}

/**
 * 待确认冻结总数（筛选入口的 (N) 角标）；失败静默，不影响主列表。
 *
 * 兼容性保护：后端若尚未实现 `freeze_pending` 过滤，该参数会被忽略、返回全部交易。
 * 因此先取 20 行判断返回结果里是否混有 `freeze_pending=false` 的行——有则说明过滤未生效，
 * 此时把计数置 0 并隐藏入口（宁可不显示，也不给一个错误的 (6746)）。后端补上过滤后自动恢复。
 */
async function refreshPendingCount(): Promise<void> {
  try {
    const res = await listTransactions({ freeze_pending: true, page: 1, page_size: 20 })
    const ignored = res.items.some((t) => !t.freeze_pending)
    pendingCount.value = ignored ? 0 : res.total
  } catch {
    pendingCount.value = 0
  }
}

/** 切换「待确认冻结」筛选：不影响其它筛选条件。 */
function togglePending(): void {
  pendingOnly.value = !pendingOnly.value
  txnStore.setQuery(buildQuery())
  void txnStore.fetchList(true)
  void refreshPendingCount()
}

/**
 * 单笔确认冻结（端点 75）：确认后该笔转为只读、余额恢复。
 * 后端同步物化可能 1–2s，接口超时设 10s；失败保留原状态并提示。
 */
async function onConfirmFreeze(txn: TransactionDTO): Promise<void> {
  freezeConfirming.value = true
  try {
    const res = await confirmFreeze({ transaction_ids: [txn.id], confirm: true })
    ElMessage.success(`已确认冻结，剩余待确认 ${res.pending_remaining} 笔`)
    await Promise.all([txnStore.fetchDetail(txn.id), txnStore.fetchList(), refreshPendingCount()])
  } catch (e) {
    ElMessage.error(`确认冻结失败：${errorText(e)}`)
  } finally {
    freezeConfirming.value = false
  }
}

function scheduleRefresh(): void {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    txnStore.setQuery(buildQuery())
    void txnStore.fetchList(true)
  }, 500)
}

function onFiltersChange(v: UiFilters): void {
  ui.setFilters(v)
}

watch(
  () => [ui.filters, ui.timePreset, ui.customRange],
  scheduleRefresh,
  { deep: true },
)

// ---------- 活跃筛选标签条 ----------
const activeTags = computed<FilterTag[]>(() => {
  const f = ui.filters
  const tags: FilterTag[] = []
  if (f.accountIds.length) {
    const names = f.accountIds
      .map((id) => master.accounts.find((a) => a.id === id)?.name ?? id)
      .join('、')
    tags.push({ key: 'accountIds', label: `账户：${names}` })
  }
  if (f.categoryId) tags.push({ key: 'categoryId', label: `分类：${master.categoryName(f.subCategoryId ?? f.categoryId)}` })
  if (f.tagIds.length) {
    const names = f.tagIds.map((id) => master.tags.find((t) => t.id === id)?.name ?? id).join('、')
    tags.push({ key: 'tagIds', label: `标签：${names}` })
  }
  if (f.merchantId) {
    tags.push({ key: 'merchantId', label: `商户：${master.merchants.find((m) => m.id === f.merchantId)?.name ?? f.merchantId}` })
  }
  if (f.type.length) {
    const map = { expense: '支出', income: '收入', transfer: '转账' }
    tags.push({ key: 'type', label: `类型：${f.type.map((t) => map[t]).join('、')}` })
  }
  if (f.ledgerId) tags.push({ key: 'ledgerId', label: `账本：${master.ledgers.find((l) => l.id === f.ledgerId)?.name ?? f.ledgerId}` })
  if (f.amountMin != null) tags.push({ key: 'amountMin', label: `金额 ≥ ${formatCents(f.amountMin)}` })
  if (f.amountMax != null) tags.push({ key: 'amountMax', label: `金额 ≤ ${formatCents(f.amountMax)}` })
  if (!f.includeAdjustments) tags.push({ key: 'includeAdjustments', label: '不含调整项' })
  if (f.keyword) tags.push({ key: 'keyword', label: `关键词：${f.keyword}` })
  return tags
})

function removeFilter(key: string): void {
  const patch: Record<string, unknown> = {}
  switch (key) {
    case 'accountIds':
      patch.accountIds = []
      break
    case 'categoryId':
      patch.categoryId = null
      patch.subCategoryId = null
      break
    case 'tagIds':
      patch.tagIds = []
      break
    case 'merchantId':
      patch.merchantId = null
      break
    case 'type':
      patch.type = []
      break
    case 'ledgerId':
      patch.ledgerId = null
      break
    case 'amountMin':
      patch.amountMin = null
      break
    case 'amountMax':
      patch.amountMax = null
      break
    case 'includeAdjustments':
      patch.includeAdjustments = true
      break
    case 'keyword':
      patch.keyword = ''
      break
  }
  ui.setFilters(patch as Partial<UiFilters>)
}

// ---------- 行操作 ----------
function openDetail(row: TransactionDTO): void {
  void txnStore.fetchDetail(row.id)
}

function jumpTo(id: string): void {
  void txnStore.fetchDetail(id)
}

function openEdit(row: TransactionDTO): void {
  txnStore.closeDrawer()
  txnStore.openEdit(row)
}

function openAdjust(row: TransactionDTO): void {
  adjustTarget.value = row
  adjustOpen.value = true
}

async function onRemove(row: TransactionDTO): Promise<void> {
  try {
    await txnStore.softDelete(row.id)
    ElMessage.success('已删除（可在回收站恢复）')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}

/** 编辑遇 409 FROZEN → 自动弹调整框并提示原因。 */
function onEditFrozen(txn: TransactionDTO, err: ApiError): void {
  ElMessage.warning(err.message || '该交易已进入冻结区，已切换为调整分录')
  openAdjust(txn)
}

async function onAdjusted(deltaCents: number): Promise<void> {
  void deltaCents
  await Promise.all([txnStore.fetchList(), master.loadAccounts()])
}

onMounted(() => {
  // 支持从 Dashboard / 报表钻取：query 参数落到筛选
  const q = route.query
  if (typeof q.category_id === 'string' && q.category_id) {
    const sub = master.flatCategories.find((c) => c.id === q.category_id && c.level === 2)
    ui.setFilters({
      categoryId: sub ? (sub.parent_id as string) : q.category_id,
      subCategoryId: sub ? sub.id : null,
    })
  }
  if (typeof q.start_date === 'string' && typeof q.end_date === 'string' && q.start_date && q.end_date) {
    ui.setCustom(q.start_date, q.end_date)
  }
  txnStore.setQuery(buildQuery())
  void txnStore.fetchList(true)
  void refreshPendingCount()
  // 清理一次性 query，避免刷新后重复应用
  if (Object.keys(q).length) void router.replace({ query: {} })
})
</script>

<style scoped lang="scss">
.txn-list {
  padding: 12px 16px;

  &__right {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  &__toolbar {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    flex-wrap: wrap;
    gap: 8px;
    margin-bottom: 8px;
  }

  &__table {
    height: 520px;
  }

  &__summary {
    display: flex;
    gap: 20px;
    padding: 10px 4px;
    border-top: 1px dashed var(--border-1);
    margin-top: 8px;
    font-size: 13px;
    color: var(--text-2);
  }

  .el-pagination {
    margin-top: 12px;
    justify-content: flex-end;
  }
}

/* v1.2 R6：待确认冻结筛选按钮 —— 蓝灰虚线，与已冻结的实心橙条刻意区分 */
.btn-freeze-pending {
  color: var(--pending);
  border: 1px dashed var(--pending-border);
  background: var(--pending-bg);

  &.el-button--primary {
    color: var(--accent);
    border-color: var(--accent);
    background: var(--accent-bg);
  }
}
</style>
