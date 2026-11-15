<template>
  <div class="txn-filters card">
    <div class="txn-filters__row">
      <el-select
        :model-value="value.accountIds"
        multiple
        collapse-tags
        collapse-tags-tooltip
        placeholder="账户"
        clearable
        style="width: 180px"
        @update:model-value="(v: string[]) => patch({ accountIds: v })"
      >
        <el-option v-for="a in master.activeAccounts" :key="a.id" :value="a.id" :label="a.display_name" />
      </el-select>

      <el-cascader
        :model-value="categoryPath"
        :options="categoryOptions"
        :props="{ checkStrictly: true }"
        placeholder="分类"
        clearable
        style="width: 160px"
        @update:model-value="onCategoryChange"
      />

      <el-select
        :model-value="value.tagIds"
        multiple
        collapse-tags
        placeholder="标签"
        clearable
        style="width: 150px"
        @update:model-value="(v: string[]) => patch({ tagIds: v })"
      >
        <el-option v-for="t in master.tags" :key="t.id" :value="t.id" :label="t.name" />
      </el-select>

      <el-select
        :model-value="value.merchantId"
        placeholder="商户"
        filterable
        remote
        clearable
        :remote-method="searchMerchant"
        style="width: 150px"
        @update:model-value="(v: string | null) => patch({ merchantId: v })"
      >
        <el-option v-for="m in master.merchants" :key="m.id" :value="m.id" :label="m.name" />
      </el-select>

      <el-select
        :model-value="value.type"
        multiple
        collapse-tags
        placeholder="类型"
        clearable
        style="width: 130px"
        @update:model-value="(v: TransactionType[]) => patch({ type: v })"
      >
        <el-option value="expense" label="支出" />
        <el-option value="income" label="收入" />
        <el-option value="transfer" label="转账" />
      </el-select>

      <el-select
        :model-value="value.ledgerId"
        placeholder="账本"
        clearable
        style="width: 130px"
        @update:model-value="(v: string | null) => patch({ ledgerId: v })"
      >
        <el-option v-for="l in master.ledgers" :key="l.id" :value="l.id" :label="l.name" />
      </el-select>

      <el-input
        :model-value="value.keyword"
        placeholder="关键词（备注/商户/对方）"
        clearable
        style="width: 180px"
        @update:model-value="(v: string) => patch({ keyword: v })"
      />
    </div>

    <div class="txn-filters__row">
      <el-input
        :model-value="amountMinYuan"
        placeholder="金额 ≥（元）"
        style="width: 110px"
        @update:model-value="onAmount('amountMin', $event)"
      />
      <span class="text-tertiary">—</span>
      <el-input
        :model-value="amountMaxYuan"
        placeholder="金额 ≤（元）"
        style="width: 110px"
        @update:model-value="onAmount('amountMax', $event)"
      />
      <el-checkbox
        :model-value="value.includeAdjustments"
        @update:model-value="(v: boolean) => patch({ includeAdjustments: v })"
      >
        含调整项
      </el-checkbox>
      <el-button size="small" @click="emit('reset')">重置筛选</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
/** 交易筛选器（T11 验收 2）：账户/分类/标签/商户/类型/账本/金额区间/关键词/含调整项。 */
import { computed } from 'vue'
import { useMasterStore } from '@/stores/master'
import { parseYuan } from '@/utils/money'
import type { TransactionType } from '@/api/types'
import type { UiFilters } from '@/stores/ui'

const props = defineProps<{ value: UiFilters }>()
const emit = defineEmits<{ change: [v: UiFilters]; reset: [] }>()

const master = useMasterStore()

const categoryOptions = computed(() =>
  master.categories.map((c) => ({
    value: c.id,
    label: c.name,
    children: (c.children ?? []).map((s) => ({ value: s.id, label: s.name })),
  })),
)

const categoryPath = computed(() => {
  if (!props.value.categoryId) return null
  return props.value.subCategoryId
    ? [props.value.categoryId, props.value.subCategoryId]
    : [props.value.categoryId]
})

const amountMinYuan = computed(() => (props.value.amountMin != null ? (props.value.amountMin / 100).toFixed(2) : ''))
const amountMaxYuan = computed(() => (props.value.amountMax != null ? (props.value.amountMax / 100).toFixed(2) : ''))

function patch(p: Partial<UiFilters>): void {
  emit('change', { ...props.value, ...p })
}

function onCategoryChange(v: string[] | null): void {
  patch({ categoryId: v?.[0] ?? null, subCategoryId: v?.[1] ?? null })
}

function onAmount(key: 'amountMin' | 'amountMax', yuan: string): void {
  patch({ [key]: yuan === '' ? null : parseYuan(yuan) } as Partial<UiFilters>)
}

function searchMerchant(q: string): void {
  void master.searchMerchantOptions(q)
}
</script>

<style scoped lang="scss">
.txn-filters {
  padding: 12px 16px;
  margin-bottom: 12px;

  &__row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;

    & + & {
      margin-top: 8px;
    }
  }
}
</style>
