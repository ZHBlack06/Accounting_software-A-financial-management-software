<template>
  <span :class="cls" :style="bold ? 'font-weight:600' : ''">{{ text }}</span>
</template>

<script setup lang="ts">
/** 金额展示组件：支出红 / 收入绿 / 转账灰 / 调整紫（中国习惯）。 */
import { computed } from 'vue'
import { formatCents, formatSigned } from '@/utils/money'

const props = withDefaults(
  defineProps<{
    cents: number
    type?: 'expense' | 'income' | 'transfer' | 'plain' | 'auto'
    signed?: boolean
    bold?: boolean
    isAdjustment?: boolean
  }>(),
  { type: 'plain', signed: false, bold: false, isAdjustment: false },
)

const text = computed(() => {
  if (props.signed && (props.type === 'expense' || props.type === 'income' || props.type === 'transfer')) {
    return formatSigned(props.cents, props.type)
  }
  return formatCents(props.cents)
})

const cls = computed(() => {
  if (props.isAdjustment) return 'amount-adjust'
  switch (props.type) {
    case 'expense':
      return 'amount-expense'
    case 'income':
      return 'amount-income'
    case 'transfer':
      return 'amount-transfer'
    case 'auto':
      return props.cents < 0 ? 'amount-expense' : 'amount-income'
    default:
      return ''
  }
})
</script>
