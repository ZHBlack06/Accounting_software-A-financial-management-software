<template>
  <div class="file-step">
    <div
      class="file-step__drop"
      :class="{ 'file-step__drop--active': dragging }"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
      @click="pick"
    >
      <el-icon :size="30"><UploadFilled /></el-icon>
      <div class="file-step__title">拖拽 Excel / CSV 账本到此处，或点击选择（可多选）</div>
      <div class="text-tertiary">支持 .xlsx / .xlsm / .csv，单文件 ≤30MB，一次最多 20 个；.xls 请先另存为 .xlsx</div>
      <input
        ref="inputRef"
        type="file"
        :accept="ACCEPT_EXT"
        multiple
        style="display: none"
        @change="onInputChange"
      />
    </div>

    <div class="file-step__controls">
      <el-select v-model="state.accountId" placeholder="默认账户（未映射账户列时使用）" clearable style="width: 260px">
        <el-option v-for="a in master.activeAccounts" :key="a.id" :value="a.id" :label="a.display_name" />
      </el-select>
      <el-button size="small" text @click="state.files = []">清空文件</el-button>
      <span class="text-tertiary">已选 {{ state.files.length }} 个文件</span>
    </div>

    <el-table v-if="state.files.length" :data="fileRows" size="small" class="file-step__table">
      <el-table-column prop="name" label="文件名" min-width="220" />
      <el-table-column label="大小" width="110" align="right">
        <template #default="{ row }">{{ formatSize(row.size) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="90" align="center">
        <template #default="{ $index }">
          <el-button link size="small" type="danger" @click="removeAt($index)">移除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-alert
      v-if="rejected.length"
      type="warning"
      show-icon
      :closable="false"
      style="margin-top: 8px"
      title="以下文件未加入队列"
    >
      <div v-for="(r, i) in rejected" :key="i" class="file-step__reject">{{ r }}</div>
    </el-alert>
  </div>
</template>

<script setup lang="ts">
/**
 * 步 1：选择文件（ARCHITECTURE-V12 §5.1）。
 * - 拖拽 / 点击多选；`.xls` 前端立即拦截并提示「请另存为 .xlsx 或 .csv」且不上传。
 * - 单文件 ≤30MB；最多 20 个文件（契约 §2.2 的 1..20）。
 */
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { UploadFilled } from '@element-plus/icons-vue'
import { useMasterStore } from '@/stores/master'
import { ACCEPT_EXT, checkSheetFile, formatSize } from '@/utils/sheetMapping'
import type { WizardState } from './types'

const MAX_FILES = 20

const props = defineProps<{ state: WizardState }>()
const master = useMasterStore()
const dragging = ref(false)
const rejected = ref<string[]>([])
const inputRef = ref<HTMLInputElement>()

const fileRows = computed(() => props.state.files.map((f) => ({ name: f.name, size: f.size })))

function pick(): void {
  inputRef.value?.click()
}

function onInputChange(e: Event): void {
  const input = e.target as HTMLInputElement
  if (input.files) addFiles([...input.files])
  input.value = ''
}

function onDrop(e: DragEvent): void {
  dragging.value = false
  const files = e.dataTransfer?.files
  if (files) addFiles([...files])
}

/** 统一入口：逐个校验扩展名 / 体积 / 重复，只把通过的加入队列。 */
function addFiles(files: File[]): void {
  rejected.value = []
  for (const f of files) {
    const check = checkSheetFile(f.name, f.size)
    if (!check.ok) {
      rejected.value.push(check.reason)
      ElMessage.warning(check.reason)
      continue
    }
    if (props.state.files.some((x) => x.name === f.name && x.size === f.size)) {
      rejected.value.push(`${f.name}：已在队列中`)
      continue
    }
    if (props.state.files.length >= MAX_FILES) {
      rejected.value.push(`${f.name}：一次最多 ${MAX_FILES} 个文件`)
      break
    }
    props.state.files.push(f)
  }
  // 文件集合变化后旧批次失效
  props.state.probe = null
  props.state.candidates = []
  props.state.preview = null
  props.state.result = null
  props.state.batchId = ''
}

function removeAt(index: number): void {
  props.state.files.splice(index, 1)
  props.state.probe = null
  props.state.candidates = []
  props.state.preview = null
  props.state.batchId = ''
}
</script>

<style scoped lang="scss">
.file-step {
  &__drop {
    border: 1px dashed var(--border-1);
    border-radius: var(--radius-card);
    padding: 28px 16px;
    text-align: center;
    cursor: pointer;
    color: var(--text-2);
    background: var(--bg-page);
    transition: border-color 0.2s ease, background 0.2s ease;

    &:hover,
    &--active {
      border-color: var(--accent);
      background: var(--accent-bg);
    }
  }

  &__title {
    margin: 8px 0 4px;
    font-size: 14px;
    font-weight: 600;
    color: var(--text-1);
  }

  &__controls {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    margin: 12px 0;
  }

  &__reject {
    font-size: 12px;
    line-height: 20px;
  }
}
</style>
