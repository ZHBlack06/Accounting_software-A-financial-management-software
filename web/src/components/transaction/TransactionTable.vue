<template>
  <el-table
    v-loading="loading"
    :data="items"
    :row-class-name="rowClass"
    size="default"
    height="100%"
    style="width: 100%"
    empty-text="暂无交易记录"
    @row-click="(row: TransactionDTO) => emit('detail', row)"
  >
    <el-table-column label="时间" width="180">
      <template #default="{ row }">
        <TxnTime :time="row.transaction_time" :row-precision="row.time_precision" />
      </template>
    </el-table-column>
    <el-table-column label="类型" width="90">
      <template #default="{ row }">
        <el-tag v-if="row.transaction_type === 'expense'" type="danger" size="small" effect="plain">支出</el-tag>
        <el-tag v-else-if="row.transaction_type === 'income'" type="success" size="small" effect="plain">收入</el-tag>
        <el-tag v-else type="info" size="small" effect="plain">转账</el-tag>
        <el-tag v-if="row.is_adjustment" size="small" effect="dark" class="tag-adjust">
          {{ row.adjustment_type === 'reverse' ? '冲销' : '调整' }}
        </el-tag>
      </template>
    </el-table-column>
    <el-table-column label="金额" width="120" align="right">
      <template #default="{ row }">
        <MoneyText
          :cents="row.amount_cents"
          :type="row.transaction_type"
          :signed="row.transaction_type !== 'transfer'"
          :bold="true"
          :is-adjustment="row.is_adjustment === 1"
        />
      </template>
    </el-table-column>
    <el-table-column label="账户" min-width="130">
      <template #default="{ row }">
        <span>{{ row.account?.name }}</span>
        <span v-if="row.transaction_type === 'transfer' && row.counter_account" class="text-tertiary">
          {{ row.transfer_direction === 'out' ? ' → ' : ' ← ' }}{{ row.counter_account.name }}
        </span>
      </template>
    </el-table-column>
    <el-table-column label="分类" min-width="100">
      <template #default="{ row }">
        <span v-if="row.category">{{ row.category.name }}<span v-if="row.sub_category" class="text-tertiary"> / {{ row.sub_category.name }}</span></span>
        <span v-else class="text-tertiary">—</span>
      </template>
    </el-table-column>
    <el-table-column label="商户" min-width="100">
      <template #default="{ row }">
        <span>{{ row.merchant?.name || row.merchant_text || '—' }}</span>
      </template>
    </el-table-column>
    <el-table-column label="标签" min-width="110">
      <template #default="{ row }">
        <el-tag v-for="t in row.tags" :key="t.id" size="small" effect="plain" :color="t.color + '22'" style="margin-right: 4px; border-color: transparent">
          <span :style="{ color: t.color }">{{ t.name }}</span>
        </el-tag>
      </template>
    </el-table-column>
    <el-table-column label="余额" width="120" align="right">
      <template #default="{ row }">
        <el-tooltip
          v-if="row.freeze_pending"
          content="待确认冻结的行不展示余额（确认冻结后才物化，历史值此时无意义）"
          placement="top"
        >
          <span class="text-tertiary">—</span>
        </el-tooltip>
        <span v-else-if="row.account_balance_after != null" class="text-secondary">
          {{ formatCents(row.account_balance_after) }}
        </span>
        <span v-else class="text-tertiary">—</span>
      </template>
    </el-table-column>
    <el-table-column label="操作" width="170" fixed="right">
      <template #default="{ row }">
        <!-- v1.2 R6：待确认冻结（蓝灰虚线，刻意区别于已冻结的实心橙条） -->
        <el-tooltip
          v-if="row.freeze_pending"
          content="导入的历史记录，尚未确认冻结，可直接修改；确认后转为只读"
          placement="top"
        >
          <span class="tag-freeze-pending">{{ PENDING_TAG_TEXT }}</span>
        </el-tooltip>
        <template v-if="!row.is_deleted">
          <el-tooltip :disabled="!row.is_frozen" content="冻结区交易不可直接修改，请使用调整" placement="top">
            <span>
              <el-button link size="small" type="primary" :disabled="row.is_frozen" @click.stop="emit('edit', row)">编辑</el-button>
            </span>
          </el-tooltip>
          <el-button v-if="row.is_frozen" link size="small" type="warning" @click.stop="emit('adjust', row)">调整</el-button>
          <el-popconfirm title="删除这笔交易？（软删除，可恢复）" @confirm="emit('remove', row)">
            <template #reference>
              <el-button link size="small" type="danger" :disabled="row.is_frozen" @click.stop>删除</el-button>
            </template>
          </el-popconfirm>
        </template>
        <el-tag v-else type="info" size="small">已删除</el-tag>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup lang="ts">
/**
 * 交易明细表（T11 验收 1）：冻结行左侧橙条 + 「冻结」标签；软删行灰底。
 * v1.2 T07：时间列走 TxnTime（行级 time_precision 优先；行级 day 只显示日期 + 圆点标注）。
 * v1.2 R6：`freeze_pending` 行 → 行尾蓝灰虚线「待确认冻结」标签 + 余额列显示「—」（§9.8）。
 */
import { formatCents } from '@/utils/money'
import { PENDING_TAG_TEXT } from '@/utils/freeze'
import type { TransactionDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import TxnTime from '@/components/common/TxnTime.vue'

defineProps<{ items: TransactionDTO[]; loading: boolean }>()

const emit = defineEmits<{
  detail: [row: TransactionDTO]
  edit: [row: TransactionDTO]
  adjust: [row: TransactionDTO]
  remove: [row: TransactionDTO]
}>()

function rowClass({ row }: { row: TransactionDTO }): string {
  const cls: string[] = []
  if (row.is_frozen) cls.push('row-frozen')
  if (row.is_deleted) cls.push('row-deleted')
  return cls.join(' ')
}
</script>

<style lang="scss">
/* 行级样式需非 scoped（el-table 渲染在外层） */
.row-frozen td:first-child {
  box-shadow: inset 3px 0 0 var(--frozen);
}

.row-frozen td:first-child .cell::after {
  content: '冻结';
  display: inline-block;
  margin-left: 6px;
  font-size: 11px;
  color: var(--frozen);
  border: 1px solid var(--frozen);
  border-radius: 4px;
  padding: 0 4px;
  line-height: 16px;
}

.row-deleted {
  --el-table-tr-bg-color: var(--bg-page);
  color: var(--text-3);
  text-decoration: line-through;
}

.tag-freeze-pending {
  display: inline-block;
  margin-right: 6px;
  padding: 0 6px;
  font-size: 11px;
  line-height: 18px;
  color: var(--pending);
  border: 1px dashed var(--pending-border);
  border-radius: 4px;
  background: var(--pending-bg);
  vertical-align: middle;
}

.tag-adjust {
  margin-left: 4px;
  background: var(--adjust);
  border-color: var(--adjust);
}
</style>
