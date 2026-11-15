<template>
  <div ref="el" class="chart" :style="{ height: height }" />
</template>

<script setup lang="ts">
/**
 * ECharts 容器组件（ARCHITECTURE §7.5 通用要求）：
 * onMounted init / watch option setOption / ResizeObserver 自适应 / onBeforeUnmount dispose。
 */
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { echarts } from '@/utils/echarts'
import type { ECharts, EChartsCoreOption } from 'echarts'
import { useUiStore } from '@/stores/ui'

const props = withDefaults(
  defineProps<{
    option: EChartsCoreOption
    height?: string
    /** 点击事件透传（趋势图下钻用）。 */
    onClick?: (params: unknown) => void
  }>(),
  { height: '320px', onClick: undefined },
)

const el = ref<HTMLDivElement | null>(null)
const ui = useUiStore()
let chart: ECharts | null = null
let observer: ResizeObserver | null = null

function render(): void {
  if (!chart) return
  chart.setOption(props.option, { notMerge: true })
}

onMounted(() => {
  if (!el.value) return
  chart = echarts.init(el.value)
  render()
  if (props.onClick) {
    chart.on('click', (params: unknown) => props.onClick?.(params))
  }
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(el.value)
})

watch(() => props.option, render, { deep: true })

// 深色模式切换 → 重设 option（颜色由 chartThemeVars 注入，无需重建实例）
watch(
  () => ui.isDark,
  () => render(),
)

onBeforeUnmount(() => {
  observer?.disconnect()
  observer = null
  chart?.dispose()
  chart = null
})
</script>
