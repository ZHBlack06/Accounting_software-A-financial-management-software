<template>
  <div class="card chart-card">
    <div class="chart-card__head">
      <span class="chart-card__title">高额消费</span>
      <span v-if="data" class="text-tertiary">
        阈值 {{ formatCents(data.threshold_cents) }}{{ data.auto_threshold ? '（自动 P95）' : '（配置值）' }}
      </span>
    </div>
    <ChartBlock :loading="loading" :error="error" :empty="!data || data.items.length === 0" @retry="emit('retry')">
      <div v-for="t in data?.items ?? []" :key="t.id" class="high-row" @click="emit('detail', t)">
        <TxnTime class="text-tertiary high-row__date" :time="t.transaction_time" :row-precision="t.time_precision" />
        <span class="high-row__name">{{ t.category?.name ?? '—' }}<span v-if="t.merchant" class="text-tertiary"> · {{ t.merchant.name }}</span></span>
        <MoneyText :cents="t.amount_cents" type="expense" :bold="true" class="high-row__amount" />
      </div>
    </ChartBlock>
  </div>
</template>

<script setup lang="ts">
/** 高额消费列表（T13 验收 7）：顶部展示阈值来源，点击打开详情抽屉。 */
import { formatCents } from '@/utils/money'
import type { HighAmountDTO, TransactionDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import TxnTime from '@/components/common/TxnTime.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'

defineProps<{ data: HighAmountDTO | null; loading: boolean; error: string | null }>()
const emit = defineEmits<{ detail: [txn: TransactionDTO]; retry: [] }>()
</script>

<style scoped lang="scss">
.high-row {
  display: grid;
  grid-template-columns: 110px 1fr 110px;
  gap: 8px;
  align-items: center;
  padding: 7px 4px;
  font-size: 13px;
  cursor: pointer;
  border-radius: 6px;

  &:hover {
    background: var(--bg-page);
  }

  &__amount {
    text-align: right;
  }

  &__name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}
</style>
