<template>
  <div v-if="tags.length > 0" class="filter-tag-bar">
    <span class="filter-tag-bar__label">筛选：</span>
    <el-tag
      v-for="t in tags"
      :key="t.key"
      closable
      size="small"
      class="filter-tag-bar__tag"
      @close="emit('remove', t.key)"
    >
      {{ t.label }}
    </el-tag>
    <el-button link type="primary" size="small" @click="emit('clear')">清空</el-button>
  </div>
</template>

<script setup lang="ts">
/** 活跃筛选条件标签条（PRD 4.7）：可单个移除 / 一键清空。 */
export interface FilterTag {
  key: string
  label: string
}

defineProps<{ tags: FilterTag[] }>()
const emit = defineEmits<{ remove: [key: string]; clear: [] }>()
</script>

<style scoped lang="scss">
.filter-tag-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 6px 0;

  &__label {
    font-size: 12px;
    color: var(--text-3);
  }
}
</style>
