<template>
  <div class="settings-view">
    <PageHeader title="设置" subtitle="账户 / 分类 / 标签 / 商户 / 账本 / 规则 / 系统 / 备份" />
    <div class="card">
      <el-tabs v-model="tab">
        <el-tab-pane label="账户" name="accounts"><AccountSettings v-if="mountedTabs.accounts" /></el-tab-pane>
        <el-tab-pane label="分类" name="categories"><CategorySettings v-if="mountedTabs.categories" /></el-tab-pane>
        <el-tab-pane label="标签" name="tags"><TagSettings v-if="mountedTabs.tags" /></el-tab-pane>
        <el-tab-pane label="商户" name="merchants"><MerchantSettings v-if="mountedTabs.merchants" /></el-tab-pane>
        <el-tab-pane label="账本" name="ledgers"><LedgerSettings v-if="mountedTabs.ledgers" /></el-tab-pane>
        <el-tab-pane label="规则" name="rules"><LedgerSettings v-if="mountedTabs.rules" /></el-tab-pane>
        <el-tab-pane label="系统" name="system"><SystemSettings v-if="mountedTabs.system" /></el-tab-pane>
        <el-tab-pane label="备份" name="backup"><BackupSettings v-if="mountedTabs.backup" /></el-tab-pane>
      </el-tabs>
    </div>

    <!-- 版本信息：低调一行，数据来自 /api/meta（不硬编码） -->
    <div class="settings-view__version">
      <span>个人记账 {{ versionText }}</span>
      <span v-if="meta.schemaVersion" class="settings-view__sep">·</span>
      <span v-if="meta.schemaVersion">数据结构 v{{ meta.schemaVersion }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 设置页（T10）：8 个 tab，按需挂载（切过的 tab 保活，保留各自状态）。
 * 页脚显示当前后端版本（v1.2 新增）：值取自 /api/meta 的 app_version（回退 version），
 * 前端不硬编码版本号，用户一眼能看出自己跑的是 1.1 还是 1.2。
 */
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useMasterStore } from '@/stores/master'
import { useMetaStore } from '@/stores/meta'
import PageHeader from '@/components/common/PageHeader.vue'
import AccountSettings from '@/components/settings/AccountSettings.vue'
import CategorySettings from '@/components/settings/CategorySettings.vue'
import TagSettings from '@/components/settings/TagSettings.vue'
import MerchantSettings from '@/components/settings/MerchantSettings.vue'
import LedgerSettings from '@/components/settings/LedgerSettings.vue'
import SystemSettings from '@/components/settings/SystemSettings.vue'
import BackupSettings from '@/components/settings/BackupSettings.vue'

type TabKey = 'accounts' | 'categories' | 'tags' | 'merchants' | 'ledgers' | 'rules' | 'system' | 'backup'

const meta = useMetaStore()
const tab = ref<TabKey>('accounts')

/** 只显示版本号本身（后端 version 已是 '1.2.0' 形式）。未拿到时显示占位，不猜测。 */
const versionText = computed(() => {
  const v = meta.appVersion || meta.version
  if (!v) return 'v—'
  return v.startsWith('v') ? v : `v${v}`
})
const mountedTabs = reactive<Record<TabKey, boolean>>({
  accounts: true,
  categories: false,
  tags: false,
  merchants: false,
  ledgers: false,
  rules: false,
  system: false,
  backup: false,
})

watch(tab, (t) => {
  mountedTabs[t] = true
})

onMounted(async () => {
  const master = useMasterStore()
  // AppLayout 启动时已拉过 meta；这里兜底一次（直接进设置页/刷新场景）
  if (!meta.loaded) {
    try {
      await meta.load()
    } catch {
      /* 失败时页脚显示占位，不阻塞设置页 */
    }
  }
  if (!master.loaded.categories) await master.loadCategories()
})
</script>

<style scoped lang="scss">
.settings-view {
  &__version {
    margin-top: 12px;
    text-align: center;
    font-size: 12px;
    color: var(--text-3);
    user-select: text;
  }

  &__sep {
    margin: 0 6px;
  }
}
</style>
