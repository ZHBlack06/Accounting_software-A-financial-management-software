<template>
  <el-container class="app-layout">
    <el-header class="app-header" height="56px">
      <div class="app-header__left">
        <el-button text class="app-header__menu-btn" @click="menuOpen = true">
          <el-icon :size="20"><Menu /></el-icon>
        </el-button>
        <span class="app-header__title">个人记账</span>
      </div>
      <div class="app-header__right">
        <el-button type="primary" @click="quickEntryOpen = true">
          <el-icon style="margin-right: 4px"><Plus /></el-icon>记一笔
        </el-button>
        <el-button @click="fullEntryOpen = true">完整表单</el-button>
        <el-tooltip :content="ui.isDark ? '切换浅色' : '切换深色'" placement="bottom">
          <el-button text @click="ui.toggleTheme()">
            <el-icon :size="18"><Sunny v-if="ui.isDark" /><Moon v-else /></el-icon>
          </el-button>
        </el-tooltip>
        <el-button text @click="logout">退出</el-button>
      </div>
    </el-header>

    <el-main class="app-main">
      <router-view />
    </el-main>

    <!-- 抽屉菜单：高度限制 + 滚动（PRD 4.2） -->
    <el-drawer v-model="menuOpen" direction="ltr" size="240px" :with-header="false">
      <div class="menu">
        <div class="menu__title">菜单</div>
        <div
          v-for="item in menuItems"
          :key="item.path"
          class="menu__item"
          :class="{ 'menu__item--active': route.path === item.path }"
          @click="go(item.path)"
        >
          <el-icon><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>
        </div>
        <div class="menu__footer">
          <el-switch
            :model-value="ui.isDark"
            inline-prompt
            active-text="深"
            inactive-text="浅"
            @change="ui.toggleTheme()"
          />
          <span class="text-tertiary">深色模式</span>
        </div>
        <!-- 版本水印：数据来自 /api/meta，不硬编码（v1.2 起用于区分 1.1/1.2 实例） -->
        <div v-if="appVersionText" class="menu__version">{{ appVersionText }}</div>
      </div>
    </el-drawer>

    <QuickEntryDialog v-model="quickEntryOpen" @frozen-conflict="onFrozenConflict" />

    <el-dialog
      v-model="fullEntryOpen"
      title="记一笔（完整表单）"
      width="min(880px, 94vw)"
      class="dlg--wide"
      :close-on-click-modal="false"
      append-to-body
    >
      <TransactionForm v-if="fullEntryOpen" ref="fullFormRef" @saved="onFullSaved" @frozen-conflict="onFrozenConflict" />
      <template #footer>
        <el-button @click="fullEntryOpen = false">取消</el-button>
        <el-button type="primary" @click="fullFormRef?.submit()">保存</el-button>
      </template>
    </el-dialog>
  </el-container>
</template>

<script setup lang="ts">
/**
 * 三段式骨架（T09 / PRD 4.1-4.2）：顶部栏（记一笔 / 完整表单 / 菜单）+ 内容区。
 * 全局快捷键 Ctrl+N / Alt+N 呼出快速记账；深色模式切换；登录态与主数据初始化。
 * v1.2 T07：meta 落 metaStore（含 time_precision），全站时间展示按精度收敛。
 * v1.2 T08：「完整表单」弹窗加宽到 min(880px, 94vw) 并标记 dlg--wide。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Menu, Plus, Sunny, Moon, HomeFilled, List, PieChart, Upload, Setting } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import { useMasterStore } from '@/stores/master'
import { useMetaStore } from '@/stores/meta'
import { fetchMeta } from '@/api/meta'
import { errorText, type ApiError } from '@/api/client'
import QuickEntryDialog from '@/components/entry/QuickEntryDialog.vue'
import TransactionForm from '@/components/entry/TransactionForm.vue'
import type { TransactionCreateResult } from '@/api/types'

const auth = useAuthStore()
const ui = useUiStore()
const master = useMasterStore()
const metaStore = useMetaStore()
const route = useRoute()
const router = useRouter()

const menuOpen = ref(false)
const quickEntryOpen = ref(false)
const fullEntryOpen = ref(false)
const fullFormRef = ref<InstanceType<typeof TransactionForm>>()

/** 侧边栏底部的版本水印（meta.app_version ?? meta.version）。 */
const appVersionText = computed(() => {
  const v = metaStore.appVersion || metaStore.version
  return v ? `个人记账 ${v.startsWith('v') ? v : `v${v}`}` : ''
})

const menuItems = [
  { path: '/', label: '首页', icon: HomeFilled },
  { path: '/transactions', label: '明细', icon: List },
  { path: '/reports', label: '报表', icon: PieChart },
  { path: '/import', label: '导入', icon: Upload },
  { path: '/settings', label: '设置', icon: Setting },
]

function go(path: string): void {
  menuOpen.value = false
  void router.push(path)
}

function logout(): void {
  auth.logout()
  void router.replace('/login')
}

/** 全局快捷键：Ctrl+N / Alt+N 呼出快速记账。 */
function onKeydown(e: KeyboardEvent): void {
  if ((e.ctrlKey || e.altKey) && (e.key === 'n' || e.key === 'N')) {
    e.preventDefault()
    quickEntryOpen.value = true
  }
}

function onFullSaved(result: TransactionCreateResult | null): void {
  fullEntryOpen.value = false
  if (result) ElMessage.success('已保存')
}

/** 冻结区写入冲突：提示并引导（编辑场景的 AdjustDialog 由 TransactionsView 处理）。 */
function onFrozenConflict(err: ApiError): void {
  ElMessage.warning(err.message || '该交易已进入冻结区，请到明细页使用调整分录')
}

onMounted(async () => {
  ui.applyTheme()
  window.addEventListener('keydown', onKeydown)
  try {
    const dto = await fetchMeta()
    metaStore.applyDto(dto)
    ui.syncFromMeta(dto.ui_prefs)
    ui.applyTheme()
  } catch (e) {
    ElMessage.warning(`基础信息加载失败：${errorText(e)}`)
  }
  await master.loadAll()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
})
</script>

<style scoped lang="scss">
.app-layout {
  min-height: 100vh;
}

.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border-2);
  padding: 0 16px;
  position: sticky;
  top: 0;
  z-index: 10;

  &__left,
  &__right {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  &__title {
    font-size: 16px;
    font-weight: 600;
    margin-left: 4px;
  }
}

.app-main {
  padding: 16px;
  max-width: 1200px;
  width: 100%;
  margin: 0 auto;
}

.menu {
  display: flex;
  flex-direction: column;
  height: 100%;

  &__title {
    font-size: 15px;
    font-weight: 600;
    padding: 16px 16px 8px;
  }

  &__item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 16px;
    cursor: pointer;
    color: var(--text-2);
    border-radius: 8px;
    margin: 2px 8px;

    &:hover {
      background: var(--accent-bg);
      color: var(--accent);
    }

    &--active {
      background: var(--accent-bg);
      color: var(--accent);
      font-weight: 600;
    }
  }

  &__version {
    padding: 0 16px 14px;
    font-size: 11px;
    color: var(--text-3);
    user-select: text;
  }

  &__footer {
    margin-top: auto;
    padding: 16px;
    display: flex;
    align-items: center;
    gap: 8px;
  }
}
</style>
