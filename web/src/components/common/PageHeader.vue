<template>
  <div class="page-header">
    <div class="page-header__main">
      <h2 class="page-header__title">{{ title }}</h2>
      <span v-if="subtitle" class="page-header__sub">{{ subtitle }}</span>
    </div>
    <div class="page-header__extra">
      <slot name="extra" />
    </div>
  </div>
</template>

<script setup lang="ts">
withDefaults(defineProps<{ title: string; subtitle?: string }>(), { subtitle: '' })
</script>

<style scoped lang="scss">
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;

  &__title {
    margin: 0;
    font-size: 18px;
    font-weight: 600;
    color: var(--text-1);
  }

  &__sub {
    margin-left: 8px;
    font-size: 12px;
    color: var(--text-3);
  }
}
</style>
