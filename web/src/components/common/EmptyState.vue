<template>
  <div class="empty-state">
    <el-empty :description="description" :image-size="imageSize">
      <el-button v-if="actionText" type="primary" @click="emit('action')">{{ actionText }}</el-button>
    </el-empty>
  </div>
</template>

<script setup lang="ts">
/** 统一空态（ARCHITECTURE §7.7）。 */
withDefaults(
  defineProps<{ description?: string; actionText?: string; imageSize?: number }>(),
  { description: '暂无数据', actionText: '', imageSize: 120 },
)
const emit = defineEmits<{ action: [] }>()
</script>

<style scoped lang="scss">
.empty-state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 32px 0;
}
</style>
