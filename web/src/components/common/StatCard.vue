<template>
  <div class="stat-card card" :class="`stat-card--${variant}`">
    <div class="stat-card__label">{{ label }}</div>
    <div class="stat-card__value">
      <MoneyText :cents="cents" :bold="true" />
    </div>
    <div v-if="hint" class="stat-card__hint">{{ hint }}</div>
  </div>
</template>

<script setup lang="ts">
/** Dashboard 摘要卡（收入/支出/结余/总余额）。 */
import MoneyText from './MoneyText.vue'

withDefaults(
  defineProps<{
    label: string
    cents: number
    variant?: 'income' | 'expense' | 'net' | 'balance'
    hint?: string
  }>(),
  { variant: 'balance', hint: '' },
)
</script>

<style scoped lang="scss">
.stat-card {
  min-width: 0;

  &__label {
    font-size: 13px;
    color: var(--text-2);
    margin-bottom: 6px;
  }

  &__value {
    font-size: 22px;
    line-height: 1.3;
    color: var(--text-1);
  }

  &__hint {
    margin-top: 6px;
    font-size: 12px;
    color: var(--text-3);
  }

  &--income &__value {
    color: var(--income);
  }

  &--expense &__value {
    color: var(--expense);
  }

  &--net &__value {
    color: var(--accent);
  }
}
</style>
