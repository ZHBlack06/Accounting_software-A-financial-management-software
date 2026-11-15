<template>
  <div class="chart-block">
    <div v-if="error" class="chart-block__state">
      <el-result icon="error" :title="error" :sub-title="'该图表加载失败，不影响其他区块'">
        <template #extra>
          <el-button size="small" type="primary" @click="emit('retry')">重试</el-button>
        </template>
      </el-result>
    </div>
    <div v-else-if="!loading && empty" class="chart-block__state">
      <EmptyState description="该时间范围内暂无数据" />
    </div>
    <div v-show="!error && (loading || !empty)" v-loading="loading" class="chart-block__body">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
/** 图表块三态包装：加载 / 错误（独立错误态 + 重试）/ 空态，避免整页白屏（T13 验收 10）。 */
import EmptyState from './EmptyState.vue'

defineProps<{ loading: boolean; error: string | null; empty: boolean }>()
const emit = defineEmits<{ retry: [] }>()
</script>

<style scoped lang="scss">
.chart-block {
  &__state {
    height: 300px;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  &__body {
    min-height: 300px;
  }
}
</style>
