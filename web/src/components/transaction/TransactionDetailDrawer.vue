<template>
  <el-drawer
    :model-value="open"
    size="460px"
    :with-header="false"
    @update:model-value="emit('update:open', $event)"
  >
    <div v-loading="loading" class="txn-detail">
      <template v-if="txn">
        <!-- v1.2 R6：待确认冻结行的说明 + 单笔确认 -->
        <el-alert
          v-if="txn.freeze_pending"
          type="warning"
          show-icon
          :closable="false"
          class="txn-detail__pending"
          title="本笔来自外部导入，尚未确认冻结，可直接修改"
        >
          <div class="text-tertiary">
            来源批次 {{ txn.import_batch_id || '—' }}；确认后该笔转为只读，修改需使用调整分录。
          </div>
          <el-button
            type="primary"
            size="small"
            style="margin-top: 8px"
            :loading="confirming"
            @click="emit('confirm-freeze', txn)"
          >
            确认此笔
          </el-button>
        </el-alert>
        <div class="txn-detail__head">
          <div>
            <MoneyText
              :cents="txn.amount_cents"
              :type="txn.transaction_type"
              :signed="txn.transaction_type !== 'transfer'"
              :bold="true"
              :is-adjustment="txn.is_adjustment === 1"
              class="txn-detail__amount"
            />
            <div class="txn-detail__tags">
              <el-tag v-if="txn.is_frozen" size="small" type="warning">冻结</el-tag>
              <el-tag v-if="txn.is_adjustment" size="small" class="tag-adjust-inline">
                {{ txn.adjustment_type === 'reverse' ? '冲销' : '调整' }}
              </el-tag>
              <el-tag v-if="txn.is_deleted" size="small" type="info">已删除</el-tag>
            </div>
          </div>
          <div class="txn-detail__actions">
            <el-tooltip :disabled="!txn.is_frozen" content="冻结区交易不可直接修改，请使用调整" placement="bottom">
              <span>
                <el-button size="small" :disabled="txn.is_frozen || txn.is_deleted" @click="emit('edit', txn)">编辑</el-button>
              </span>
            </el-tooltip>
            <el-button
              v-if="txn.is_frozen && !txn.is_deleted"
              size="small"
              type="warning"
              @click="emit('adjust', txn)"
            >
              调整
            </el-button>
            <el-button size="small" text @click="emit('update:open', false)">关闭</el-button>
          </div>
        </div>

        <el-descriptions :column="1" border size="small" class="txn-detail__desc">
          <el-descriptions-item label="时间">
            <TxnTime :time="txn.transaction_time" :row-precision="txn.time_precision" full />
          </el-descriptions-item>
          <el-descriptions-item label="类型">{{ typeLabel }}</el-descriptions-item>
          <el-descriptions-item label="账户">
            {{ txn.account?.name }}
            <template v-if="txn.transaction_type === 'transfer' && txn.counter_account">
              {{ txn.transfer_direction === 'out' ? '→' : '←' }} {{ txn.counter_account.name }}
            </template>
          </el-descriptions-item>
          <el-descriptions-item v-if="txn.category" label="分类">
            {{ txn.category.name }}<template v-if="txn.sub_category"> / {{ txn.sub_category.name }}</template>
          </el-descriptions-item>
          <el-descriptions-item v-if="txn.merchant || txn.merchant_text" label="商户">
            {{ txn.merchant?.name || txn.merchant_text }}
          </el-descriptions-item>
          <el-descriptions-item v-if="txn.tags.length" label="标签">
            <el-tag v-for="t in txn.tags" :key="t.id" size="small" style="margin-right: 4px">{{ t.name }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item v-if="txn.region" label="地区">{{ txn.region.name }}</el-descriptions-item>
          <el-descriptions-item v-if="txn.counterparty_name" label="对方">
            {{ txn.counterparty_name }}<template v-if="txn.counterparty_account">（{{ txn.counterparty_account }}）</template>
          </el-descriptions-item>
          <el-descriptions-item v-if="txn.remark" label="备注">{{ txn.remark }}</el-descriptions-item>
          <el-descriptions-item label="来源">{{ sourceLabel }}</el-descriptions-item>
          <el-descriptions-item v-if="txn.account_balance_after != null" label="账户余额">
            {{ formatCents(txn.account_balance_after) }}
          </el-descriptions-item>
          <el-descriptions-item v-if="txn.total_balance_after != null" label="总余额">
            {{ formatCents(txn.total_balance_after) }}
          </el-descriptions-item>
          <el-descriptions-item label="业务 ID">{{ txn.id }}</el-descriptions-item>
        </el-descriptions>

        <template v-if="txn.items && txn.items.length">
          <div class="txn-detail__section">物品明细</div>
          <el-table :data="txn.items" size="small">
            <el-table-column prop="name" label="名称" min-width="100" />
            <el-table-column label="原价" width="90" align="right">
              <template #default="{ row }">{{ formatCents(row.original_amount_cents) }}</template>
            </el-table-column>
            <el-table-column label="实付" width="90" align="right">
              <template #default="{ row }">{{ formatCents(row.paid_amount_cents) }}</template>
            </el-table-column>
            <el-table-column prop="discount_reason" label="折扣缘由" min-width="80" />
          </el-table>
        </template>

        <template v-if="linked.length">
          <div class="txn-detail__section">关联转账</div>
          <div v-for="l in linked" :key="l.id" class="txn-detail__link" @click="emit('jump', l.id)">
            {{ l.transaction_time }} {{ l.account?.name }}
            <MoneyText :cents="l.signed_cents" type="auto" />
          </div>
        </template>

        <template v-if="adjustments.length">
          <div class="txn-detail__section">关联调整项</div>
          <div v-for="a in adjustments" :key="a.id" class="txn-detail__link" @click="emit('jump', a.id)">
            <el-tag size="small" class="tag-adjust-inline">{{ a.adjustment_type === 'reverse' ? '冲销' : '调整' }}</el-tag>
            {{ a.transaction_time }}
            <MoneyText :cents="a.signed_cents" type="auto" />
            <span class="text-tertiary">{{ a.remark }}</span>
          </div>
        </template>
      </template>
      <el-empty v-else-if="!loading" description="未找到交易详情" />
    </div>
  </el-drawer>
</template>

<script setup lang="ts">
/**
 * 详情抽屉（T11 验收 5/6）：全部字段 + 物品明细 + 标签 + 关联调整项（可跳转原交易）。
 * v1.2 T07：时间走 TxnTime（行级精度优先，行级 day 只显示日期 + 圆点 + tooltip）。
 */
import { computed } from 'vue'
import { formatCents } from '@/utils/money'
import type { TransactionDetailDTO, TransactionDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import TxnTime from '@/components/common/TxnTime.vue'

const props = defineProps<{
  open: boolean
  loading: boolean
  detail: TransactionDetailDTO | null
  /** 单笔确认冻结请求中（后端同步物化，可能 1–2s）。 */
  confirming?: boolean
}>()

const emit = defineEmits<{
  'update:open': [v: boolean]
  edit: [txn: TransactionDTO]
  adjust: [txn: TransactionDTO]
  jump: [id: string]
  /** v1.2 R6：确认这一笔的冻结状态。 */
  'confirm-freeze': [txn: TransactionDTO]
}>()

const txn = computed<TransactionDTO | null>(() => props.detail?.transaction ?? null)
const linked = computed<TransactionDTO[]>(() => props.detail?.linked ?? [])
const adjustments = computed<TransactionDTO[]>(() => props.detail?.adjustments ?? [])

const typeLabel = computed(() => {
  switch (txn.value?.transaction_type) {
    case 'expense':
      return '支出'
    case 'income':
      return '收入'
    case 'transfer':
      return '内部转账'
    default:
      return '—'
  }
})

const sourceLabel = computed(() => {
  const map: Record<string, string> = {
    manual: '手动记账',
    csv: 'CSV 导入',
    ocr: '截图识别',
    adjustment: '调整分录',
    import: '导入',
  }
  return map[txn.value?.source ?? ''] ?? txn.value?.source ?? '—'
})
</script>

<style scoped lang="scss">
.txn-detail {
  padding: 4px;

  &__head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    margin-bottom: 16px;
  }

  &__amount {
    font-size: 26px;
  }

  &__tags {
    margin-top: 6px;
    display: flex;
    gap: 4px;
  }

  &__actions {
    display: flex;
    gap: 4px;
  }

  &__desc {
    margin-bottom: 12px;
  }

  &__pending {
    margin-bottom: 12px;
    text-align: left;
  }

  &__section {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-2);
    margin: 12px 0 6px;
  }

  &__link {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    border-radius: 6px;
    cursor: pointer;
    font-size: 13px;

    &:hover {
      background: var(--accent-bg);
    }
  }
}

.tag-adjust-inline {
  background: var(--adjust);
  border-color: var(--adjust);
  color: #fff;
}
</style>
