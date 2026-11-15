<template>
  <span :class="cls">{{ text }}</span>
</template>

<script setup lang="ts">
/** 环比/同比百分比：升绿降红（支出语义反向由调用方决定，此处统一升绿降红），prev=0 显示 "—"。 */
import { computed } from 'vue'

const props = defineProps<{ pct: number | null; cents: number | null }>()

const text = computed(() => {
  if (props.pct === null || props.pct === undefined) return '—'
  const sign = props.pct > 0 ? '+' : ''
  return `${sign}${props.pct.toFixed(2)}%`
})

const cls = computed(() => {
  if (props.pct === null || props.pct === undefined) return 'text-tertiary'
  return props.pct >= 0 ? 'amount-income' : 'amount-expense'
})
</script>
