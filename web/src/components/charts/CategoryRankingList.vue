<template>
  <div class="card chart-card">
    <div class="chart-card__head">
      <span class="chart-card__title">分类排行</span>
      <el-radio-group :model-value="txnType" size="small" @update:model-value="emit('update:txnType', $event as 'expense' | 'income')">
        <el-radio-button value="expense">支出</el-radio-button>
        <el-radio-button value="income">收入</el-radio-button>
      </el-radio-group>
    </div>
    <ChartBlock :loading="loading" :error="error" :empty="!items || items.length === 0" @retry="emit('retry')">
      <div v-for="item in items ?? []" :key="item.category_id" class="rank-row">
        <div class="rank-row__main" @click="toggle(item.category_id)">
          <span class="rank-row__rank">{{ item.rank }}</span>
          <span class="rank-row__name">{{ item.name }}</span>
          <el-progress
            class="rank-row__bar"
            :percentage="maxAmount ? Math.round((item.amount_cents / maxAmount) * 100) : 0"
            :stroke-width="6"
            :color="barColor(item.category_id)"
            :show-text="false"
          />
          <MoneyText :cents="item.amount_cents" :bold="true" class="rank-row__amount" />
          <span class="text-tertiary rank-row__pct">{{ item.percent.toFixed(1) }}%</span>
        </div>
        <div v-if="expandedId === item.category_id" v-loading="detailLoading" class="rank-row__detail">
          <div v-for="t in detailItems" :key="t.id" class="rank-row__txn" @click="emit('detail', t)">
            <TxnTime class="text-tertiary" :time="t.transaction_time" :row-precision="t.time_precision" />
            <span>{{ t.merchant?.name || t.remark || '—' }}</span>
            <MoneyText :cents="t.amount_cents" :type="t.transaction_type" :signed="t.transaction_type !== 'transfer'" />
          </div>
          <el-button v-if="detailItems.length" link type="primary" size="small" @click="emit('viewAll', item.category_id)">
            查看全部 →
          </el-button>
        </div>
      </div>
    </ChartBlock>
  </div>
</template>

<script setup lang="ts">
/** 分类排行（T13 验收 6）：金额降序、行内进度条、点击展开近 20 笔明细。 */
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { listTransactions } from '@/api/transactions'
import { errorText } from '@/api/client'
import { PALETTE } from '@/utils/echarts'
import { useMasterStore } from '@/stores/master'
import { useUiStore } from '@/stores/ui'
import type { RankItem, TransactionDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import TxnTime from '@/components/common/TxnTime.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'

const props = defineProps<{
  items: RankItem[] | null
  loading: boolean
  error: string | null
  txnType: 'expense' | 'income'
}>()

const emit = defineEmits<{
  'update:txnType': [v: 'expense' | 'income']
  detail: [txn: TransactionDTO]
  viewAll: [categoryId: string]
  retry: []
}>()

const master = useMasterStore()
const ui = useUiStore()
const expandedId = ref<string | null>(null)
const detailItems = ref<TransactionDTO[]>([])
const detailLoading = ref(false)

const maxAmount = computed(() => Math.max(0, ...(props.items ?? []).map((i) => i.amount_cents)))

function barColor(categoryId: string): string {
  return master.categoryColor(categoryId) ?? PALETTE[0]
}

async function toggle(categoryId: string): Promise<void> {
  if (expandedId.value === categoryId) {
    expandedId.value = null
    return
  }
  expandedId.value = categoryId
  detailLoading.value = true
  try {
    const res = await listTransactions({
      category_id: categoryId,
      start_time: ui.range.start,
      end_time: ui.range.end,
      page: 1,
      page_size: 20,
      sort: 'time_desc',
    })
    detailItems.value = res.items
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    detailLoading.value = false
  }
}
</script>

<style scoped lang="scss">
.rank-row {
  &__main {
    display: grid;
    grid-template-columns: 28px 80px 1fr 110px 52px;
    align-items: center;
    gap: 8px;
    padding: 6px 4px;
    cursor: pointer;
    border-radius: 6px;

    &:hover {
      background: var(--bg-page);
    }
  }

  &__rank {
    color: var(--text-3);
    font-size: 12px;
  }

  &__name {
    font-size: 13px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__amount {
    text-align: right;
    font-size: 13px;
  }

  &__pct {
    text-align: right;
  }

  &__detail {
    padding: 4px 8px 10px 36px;
    border-left: 2px solid var(--border-2);
    margin-left: 14px;
  }

  &__txn {
    display: grid;
    grid-template-columns: 110px 1fr 100px;
    gap: 8px;
    padding: 4px 0;
    font-size: 13px;
    cursor: pointer;
    border-radius: 4px;

    &:hover {
      background: var(--accent-bg);
    }
  }
}
</style>
