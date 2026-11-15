<template>
  <div class="dashboard">
    <!-- 顶部：时间范围 + 摘要卡 2×2 -->
    <div class="card dashboard__topbar">
      <TimeRangePicker
        :preset="ui.timePreset"
        :custom-range="ui.customRange"
        @update:preset="ui.setPreset($event)"
        @update:custom-range="(r) => (ui.customRange = r)"
        @change="refreshAll"
      />
    </div>

    <div class="dashboard__stats">
      <template v-if="stats.summary.error">
        <div class="card dashboard__stat-error">
          <span>摘要加载失败：{{ stats.summary.error }}</span>
          <el-button size="small" @click="stats.fetchSummary()">重试</el-button>
        </div>
      </template>
      <template v-else>
        <StatCard label="收入" :cents="stats.summary.data?.income_cents ?? 0" variant="income"
          :hint="adjustHint" />
        <StatCard label="支出" :cents="stats.summary.data?.expense_cents ?? 0" variant="expense"
          :hint="stats.summary.data ? `日均支出 ${formatCents(stats.summary.data.avg_expense_per_day_cents)}` : ''" />
        <StatCard label="结余" :cents="stats.summary.data?.net_cents ?? 0" variant="net"
          :hint="stats.summary.data ? `${stats.summary.data.transaction_count} 笔 / ${stats.summary.data.day_count} 天` : ''" />
        <StatCard label="总余额" :cents="stats.summary.data?.total_balance_cents ?? master.totalBalanceCents" variant="balance" />
      </template>
    </div>

    <!-- 中部图表区（竖向滚动） -->
    <div class="dashboard__charts">
      <TrendChart
        :data="stats.trend.data"
        :loading="stats.trend.loading"
        :error="stats.trend.error"
        :chart-type="ui.trendChartType"
        :granularity="trendGranularity"
        @update:chart-type="ui.setTrendChartType($event)"
        @update:granularity="onGranularityChange"
        @drill="onTrendDrill"
        @retry="stats.fetchTrend(trendGranularity)"
      />
      <CategoryPieChart
        :data="stats.breakdown.data"
        :loading="stats.breakdown.loading"
        :error="stats.breakdown.error"
        :pie-type="ui.pieChartType"
        :txn-type="stats.breakdownType"
        @update:pie-type="ui.setPieChartType($event)"
        @update:txn-type="onBreakdownTypeChange"
        @drill="drillToTransactions"
        @retry="stats.fetchBreakdown()"
      />
      <AccountBalanceChart
        :data="stats.series.data"
        :loading="stats.series.loading"
        :error="stats.series.error"
        :accounts="master.activeAccounts"
        :selected-ids="stats.seriesAccountIds"
        @update:selected-ids="stats.setSeriesAccounts($event)"
        @retry="stats.fetchSeries()"
      />
      <div class="dashboard__charts-row">
        <CategoryRankingList
          :items="stats.ranking.data"
          :loading="stats.ranking.loading"
          :error="stats.ranking.error"
          :txn-type="stats.rankingType"
          @update:txn-type="onRankingTypeChange"
          @detail="openDetail"
          @view-all="drillToTransactions"
          @retry="stats.fetchRanking()"
        />
        <HighAmountList
          :data="stats.highAmount.data"
          :loading="stats.highAmount.loading"
          :error="stats.highAmount.error"
          @detail="openDetail"
          @retry="stats.fetchHighAmount()"
        />
      </div>
    </div>

    <!-- 底部明细区（最近交易） -->
    <div class="card dashboard__recent">
      <div class="chart-card__head">
        <span class="chart-card__title">
          最近交易
          <el-tag v-if="drillDate" size="small" closable type="warning" style="margin-left: 8px" @close="clearDrill">
            {{ drillDate }}
          </el-tag>
        </span>
        <el-button link type="primary" size="small" @click="router.push('/transactions')">全部明细 →</el-button>
      </div>
      <el-table v-loading="recentLoading" :data="recentItems" size="small" @row-click="openDetail">
        <el-table-column label="时间" width="180">
          <template #default="{ row }">
            <TxnTime :time="row.transaction_time" :row-precision="row.time_precision" />
          </template>
        </el-table-column>
        <el-table-column label="类型" width="70">
          <template #default="{ row }">
            <el-tag :type="row.transaction_type === 'expense' ? 'danger' : row.transaction_type === 'income' ? 'success' : 'info'" size="small" effect="plain">
              {{ row.transaction_type === 'expense' ? '支出' : row.transaction_type === 'income' ? '收入' : '转账' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="金额" width="120" align="right">
          <template #default="{ row }">
            <MoneyText :cents="row.amount_cents" :type="row.transaction_type" :signed="row.transaction_type !== 'transfer'" :bold="true" :is-adjustment="row.is_adjustment === 1" />
          </template>
        </el-table-column>
        <el-table-column label="分类" min-width="110">
          <template #default="{ row }">{{ row.category?.name ?? '—' }}</template>
        </el-table-column>
        <el-table-column label="备注/商户" min-width="140">
          <template #default="{ row }">{{ row.remark || row.merchant?.name || '—' }}</template>
        </el-table-column>
      </el-table>
      <el-alert v-if="recentError" type="error" :title="recentError" :closable="false" style="margin-top: 8px">
        <el-button size="small" @click="fetchRecent">重试</el-button>
      </el-alert>
    </div>

    <!-- 详情抽屉（复用 T11 组件） -->
    <TransactionDetailDrawer
      :open="drawerOpen"
      :loading="detailLoading"
      :detail="detail"
      @update:open="drawerOpen = $event"
      @edit="goTransactions"
      @adjust="goTransactions"
      @jump="jumpTo"
    />
  </div>
</template>

<script setup lang="ts">
/**
 * Dashboard（T13 / PRD 栏目 4）：三段式 = 摘要卡 2×2 + 图表区 + 底部明细区。
 * 时间范围切换 → 摘要卡 + 全部图表 + 明细同时刷新（stats store 请求序号丢弃乱序响应）。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useUiStore } from '@/stores/ui'
import { useStatsStore } from '@/stores/stats'
import { useMasterStore } from '@/stores/master'
import { listTransactions, getTransaction } from '@/api/transactions'
import { formatCents } from '@/utils/money'
import TxnTime from '@/components/common/TxnTime.vue'
import type { Granularity, TransactionDTO, TransactionDetailDTO } from '@/api/types'
import TimeRangePicker from '@/components/common/TimeRangePicker.vue'
import StatCard from '@/components/common/StatCard.vue'
import MoneyText from '@/components/common/MoneyText.vue'
import TrendChart from '@/components/charts/TrendChart.vue'
import CategoryPieChart from '@/components/charts/CategoryPieChart.vue'
import AccountBalanceChart from '@/components/charts/AccountBalanceChart.vue'
import CategoryRankingList from '@/components/charts/CategoryRankingList.vue'
import HighAmountList from '@/components/charts/HighAmountList.vue'
import TransactionDetailDrawer from '@/components/transaction/TransactionDetailDrawer.vue'

const ui = useUiStore()
const stats = useStatsStore()
const master = useMasterStore()
const router = useRouter()

const trendGranularity = ref<Granularity>(ui.effectiveGranularity)
const drillDate = ref<string | null>(null)

const recentItems = ref<TransactionDTO[]>([])
const recentLoading = ref(false)
const recentError = ref<string | null>(null)

const drawerOpen = ref(false)
const detailLoading = ref(false)
const detail = ref<TransactionDetailDTO | null>(null)

const adjustHint = computed(() => {
  const adj = stats.summary.data?.adjustment_net_cents
  return adj ? `含调整净额 ${formatCents(adj)}` : ''
})

let debounceTimer: ReturnType<typeof setTimeout> | null = null

/** 时间范围变化 → 全量刷新（500ms 防抖）。 */
function refreshAll(): void {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    trendGranularity.value = ui.effectiveGranularity
    void stats.fetchAll({ ...ui.rangeQuery }, trendGranularity.value)
    void fetchRecent()
  }, 500)
}

watch(
  () => [ui.timePreset, ui.customRange],
  refreshAll,
  { deep: true },
)

function onGranularityChange(g: Granularity): void {
  trendGranularity.value = g
  void stats.fetchTrend(g)
}

function onBreakdownTypeChange(t: 'expense' | 'income'): void {
  stats.breakdownType = t
  void stats.fetchBreakdown()
}

function onRankingTypeChange(t: 'expense' | 'income'): void {
  stats.rankingType = t
  void stats.fetchRanking()
}

/** 趋势图点击某日 → 底部明细区筛选该日。 */
function onTrendDrill(bucket: string): void {
  drillDate.value = bucket
  void fetchRecent()
}

function clearDrill(): void {
  drillDate.value = null
  void fetchRecent()
}

/** 分类占比/排行 → 跳明细页钻取。 */
function drillToTransactions(categoryId: string): void {
  void router.push({
    path: '/transactions',
    query: { category_id: categoryId, start_date: ui.range.start.slice(0, 10), end_date: ui.range.end.slice(0, 10) },
  })
}

async function fetchRecent(): Promise<void> {
  recentLoading.value = true
  recentError.value = null
  try {
    const day = drillDate.value
    const res = await listTransactions({
      start_time: day ? `${day} 00:00:00` : ui.range.start,
      end_time: day ? `${day} 23:59:59` : ui.range.end,
      page: 1,
      page_size: 20,
      sort: 'time_desc',
    })
    recentItems.value = res.items
  } catch (e) {
    recentError.value = e instanceof Error ? e.message : '加载失败'
    recentItems.value = []
  } finally {
    recentLoading.value = false
  }
}

async function openDetail(row: TransactionDTO): Promise<void> {
  drawerOpen.value = true
  detailLoading.value = true
  try {
    detail.value = await getTransaction(row.id)
  } finally {
    detailLoading.value = false
  }
}

async function jumpTo(id: string): Promise<void> {
  detailLoading.value = true
  try {
    detail.value = await getTransaction(id)
  } finally {
    detailLoading.value = false
  }
}

/** Dashboard 上不提供编辑/调整，引导到明细页（保持单一编辑入口）。 */
function goTransactions(): void {
  drawerOpen.value = false
  void router.push('/transactions')
}

onMounted(() => {
  // 账户余额图默认选前 3 个账户（主数据可能尚未加载完，watch 兜底）
  if (!stats.seriesAccountIds.length) {
    stats.seriesAccountIds = master.activeAccounts.slice(0, 3).map((a) => a.id)
  }
  void stats.fetchAll({ ...ui.rangeQuery }, trendGranularity.value)
  void fetchRecent()
})

watch(
  () => master.loaded.accounts,
  (loaded) => {
    if (loaded && !stats.seriesAccountIds.length) {
      stats.seriesAccountIds = master.activeAccounts.slice(0, 3).map((a) => a.id)
      void stats.fetchSeries()
    }
  },
)
</script>

<style scoped lang="scss">
.dashboard {
  display: flex;
  flex-direction: column;
  gap: 12px;

  &__stats {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 12px;
  }

  &__stat-error {
    grid-column: 1 / -1;
    display: flex;
    align-items: center;
    gap: 12px;
    color: var(--expense);
  }

  &__charts {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  &__charts-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  &__recent {
    min-height: 200px;
  }
}
</style>
