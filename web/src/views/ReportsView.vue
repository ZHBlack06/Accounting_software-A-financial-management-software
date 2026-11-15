<template>
  <div class="reports-view">
    <PageHeader title="报表中心">
      <template #extra>
        <TimeRangePicker
          :preset="ui.timePreset"
          :custom-range="ui.customRange"
          @update:preset="ui.setPreset($event)"
          @update:custom-range="(r) => (ui.customRange = r)"
        />
      </template>
    </PageHeader>
    <div class="card">
      <el-tabs v-model="tab">
        <!-- 各 tab 用 v-show 保活，切换保留各自筛选状态（T14 验收 1） -->
        <el-tab-pane label="分类汇总" name="category" />
        <el-tab-pane label="账户汇总" name="account" />
        <el-tab-pane label="标签汇总" name="tag" />
        <el-tab-pane label="周期对比" name="period" />
      </el-tabs>
      <div v-show="tab === 'category'"><CategoryReport v-if="mounted.category" /></div>
      <div v-show="tab === 'account'"><AccountReport v-if="mounted.account" /></div>
      <div v-show="tab === 'tag'"><TagReport v-if="mounted.tag" /></div>
      <div v-show="tab === 'period'"><PeriodCompareReport v-if="mounted.period" /></div>
    </div>
  </div>
</template>

<script setup lang="ts">
/** 报表中心（T14）：4 个 tab 懒挂载 + v-show 保活，切换保留筛选状态。 */
import { reactive, ref, watch } from 'vue'
import { useUiStore } from '@/stores/ui'
import PageHeader from '@/components/common/PageHeader.vue'
import TimeRangePicker from '@/components/common/TimeRangePicker.vue'
import CategoryReport from '@/components/reports/CategoryReport.vue'
import AccountReport from '@/components/reports/AccountReport.vue'
import TagReport from '@/components/reports/TagReport.vue'
import PeriodCompareReport from '@/components/reports/PeriodCompareReport.vue'

const ui = useUiStore()
const tab = ref<'category' | 'account' | 'tag' | 'period'>('category')
const mounted = reactive({ category: true, account: false, tag: false, period: false })

watch(tab, (t) => {
  mounted[t] = true
})
</script>
