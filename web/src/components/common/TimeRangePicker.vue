<template>
  <div class="time-range-picker">
    <el-radio-group :model-value="preset" size="small" @change="onPresetChange">
      <el-radio-button
        v-for="key in PRESET_ORDER.filter((k) => k !== 'custom')"
        :key="key"
        :value="key"
      >
        {{ PRESET_LABELS[key as Exclude<TimePresetKey, 'custom'>] }}
      </el-radio-button>
      <el-radio-button value="custom">自定义</el-radio-button>
    </el-radio-group>
    <el-date-picker
      v-if="preset === 'custom'"
      v-model="customValue"
      type="daterange"
      size="small"
      range-separator="至"
      start-placeholder="开始日期"
      end-placeholder="结束日期"
      value-format="YYYY-MM-DD"
      style="margin-left: 8px; width: 260px"
      @change="onCustomChange"
    />
  </div>
</template>

<script setup lang="ts">
/**
 * 时间范围选择器（ARCHITECTURE §7.4 / PRD 4.6）：13 预设 + 自定义区间。
 * 切换后 emit('change', range)，由页面统一触发刷新。
 */
import { ref, watch } from 'vue'
import { PRESET_LABELS, PRESET_ORDER, currentRange, type TimePresetKey, type TimeRange } from '@/utils/presets'

const props = defineProps<{ preset: TimePresetKey; customRange: [string, string] | null }>()
const emit = defineEmits<{
  'update:preset': [key: TimePresetKey]
  'update:customRange': [range: [string, string] | null]
  change: [range: TimeRange]
}>()

const customValue = ref<[string, string] | null>(props.customRange)

watch(
  () => props.customRange,
  (v) => {
    customValue.value = v
  },
)

function onPresetChange(val: string | number | boolean | undefined): void {
  const key = val as TimePresetKey
  emit('update:preset', key)
  if (key !== 'custom') {
    emit('change', currentRange(key, null))
  } else if (customValue.value) {
    emit('change', currentRange('custom', customValue.value))
  }
}

function onCustomChange(val: [string, string] | null): void {
  emit('update:customRange', val)
  if (val) emit('change', currentRange('custom', val))
}
</script>

<style scoped lang="scss">
.time-range-picker {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}
</style>
