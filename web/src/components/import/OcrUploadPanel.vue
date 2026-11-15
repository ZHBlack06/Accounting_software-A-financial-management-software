<template>
  <div class="ocr-panel">
    <div
      class="ocr-panel__drop"
      :class="{ 'ocr-panel__drop--active': dragging }"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
      @click="pickFile"
    >
      <el-icon :size="28"><Picture /></el-icon>
      <div class="ocr-panel__title">拖拽账单截图、Ctrl+V 粘贴，或点击选择图片（可多选）</div>
      <div class="text-tertiary">支持 jpg / png / webp；识别为异步任务，每 1.5s 轮询</div>
      <input ref="fileInput" type="file" accept="image/*" multiple style="display: none" @change="onFileChange" />
    </div>

    <el-alert
      v-if="unavailable"
      type="warning"
      show-icon
      :closable="false"
      style="margin-top: 12px"
      title="OCR 服务未启动，可手动录入"
    >
      <el-button size="small" type="primary" @click="emit('manualFallback')">前往快捷记账</el-button>
    </el-alert>

    <div v-if="tasks.length" class="ocr-panel__list">
      <div v-for="t in tasks" :key="t.taskId" class="ocr-panel__item">
        <div class="ocr-panel__item-main">
          <span class="ocr-panel__name">{{ t.fileName }}</span>
          <el-tag size="small" :type="statusType(t.status)">
            {{ statusLabel(t.status) }}
          </el-tag>
          <span v-if="t.error" class="text-tertiary">{{ t.error }}</span>
        </div>
        <div class="ocr-panel__item-actions">
          <el-button
            v-if="t.status === 'done' && t.result"
            size="small"
            type="primary"
            link
            @click="activeTaskId = t.taskId"
          >
            查看结果
          </el-button>
          <el-button size="small" link type="danger" @click="removeTask(t.taskId)">移除</el-button>
        </div>
      </div>
    </div>

    <OcrResultCard
      v-if="activeTask?.result"
      :result="activeTask.result"
      :screenshot-id="activeTask.screenshotId"
      style="margin-top: 12px"
      @discard="removeTask(activeTask.taskId)"
      @confirm="emit('confirmed', $event)"
    />
  </div>
</template>

<script setup lang="ts">
/**
 * OCR 上传面板（T15 验收 5/7/8）：拖拽/粘贴/多选批量；异步轮询 1.5s、超时 120s；
 * 503 → 降级提示可手动录入；批量完成后逐条查看结果。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Picture } from '@element-plus/icons-vue'
import { getOcrTask, recognizeAsync } from '@/api/ocr'
import { ApiError, errorText } from '@/api/client'
import type { OcrConfirmPayload, OcrResultData, OcrTaskDTO } from '@/api/types'
import OcrResultCard from './OcrResultCard.vue'

interface LocalTask {
  taskId: string
  fileName: string
  screenshotId: string | null
  status: 'pending' | 'processing' | 'done' | 'failed'
  result: OcrResultData | null
  error: string
  startedAt: number
}

const emit = defineEmits<{
  manualFallback: []
  confirmed: [payload: OcrConfirmPayload]
}>()

const fileInput = ref<HTMLInputElement>()
const dragging = ref(false)
const unavailable = ref(false)
const tasks = ref<LocalTask[]>([])
const activeTaskId = ref<string | null>(null)

let pollTimer: ReturnType<typeof setInterval> | null = null

const activeTask = computed<LocalTask | null>(() => {
  const id = activeTaskId.value ?? tasks.value.find((t) => t.status === 'done')?.taskId ?? null
  return tasks.value.find((t) => t.taskId === id) ?? null
})

function statusLabel(s: LocalTask['status']): string {
  const map = { pending: '排队中', processing: '识别中', done: '已完成', failed: '失败' }
  return map[s]
}

function statusType(s: LocalTask['status']): 'info' | 'warning' | 'success' | 'danger' {
  const map = { pending: 'info', processing: 'warning', done: 'success', failed: 'danger' } as const
  return map[s]
}

function pickFile(): void {
  fileInput.value?.click()
}

function onFileChange(e: Event): void {
  const files = (e.target as HTMLInputElement).files
  if (files) void uploadMany(Array.from(files))
}

function onDrop(e: DragEvent): void {
  dragging.value = false
  const files = e.dataTransfer?.files
  if (files) void uploadMany(Array.from(files))
}

/** 粘贴截图（PRD 5.10 / T15 验收 5）。 */
function onPaste(e: ClipboardEvent): void {
  const items = e.clipboardData?.items
  if (!items) return
  const files: File[] = []
  for (const item of Array.from(items)) {
    if (item.kind === 'file' && item.type.startsWith('image/')) {
      const f = item.getAsFile()
      if (f) files.push(f)
    }
  }
  if (files.length) {
    e.preventDefault()
    void uploadMany(files)
  }
}

async function uploadMany(files: File[]): Promise<void> {
  for (const file of files) {
    if (!file.type.startsWith('image/')) {
      ElMessage.warning(`${file.name} 不是图片，已跳过`)
      continue
    }
    try {
      const res = await recognizeAsync(file, 'auto')
      tasks.value.push({
        taskId: res.task_id,
        fileName: file.name || '粘贴图片',
        screenshotId: res.screenshot_id ?? null,
        status: 'processing',
        result: null,
        error: '',
        startedAt: Date.now(),
      })
      unavailable.value = false
      ensurePolling()
    } catch (e) {
      if (e instanceof ApiError && e.code === 'OCR_UNAVAILABLE') {
        unavailable.value = true
        ElMessage.warning('OCR 服务未启动，可手动录入或稍后重试')
      } else {
        ElMessage.error(errorText(e))
      }
    }
  }
}

function ensurePolling(): void {
  if (pollTimer) return
  pollTimer = setInterval(() => void poll(), 1500)
}

async function poll(): Promise<void> {
  const pending = tasks.value.filter((t) => t.status === 'pending' || t.status === 'processing')
  if (!pending.length) {
    stopPolling()
    return
  }
  for (const t of pending) {
    if (Date.now() - t.startedAt > 120000) {
      t.status = 'failed'
      t.error = '识别超时（120 秒），请重试'
      continue
    }
    try {
      const task: OcrTaskDTO = await getOcrTask(t.taskId)
      if (task.status === 'done') {
        t.status = 'done'
        t.result = task.result ?? null
      } else if (task.status === 'failed') {
        t.status = 'failed'
        t.error = task.error ?? '识别失败'
      } else {
        t.status = task.status === 'pending' ? 'pending' : 'processing'
      }
    } catch (e) {
      t.status = 'failed'
      t.error = errorText(e)
    }
  }
  if (tasks.value.every((t) => t.status === 'done' || t.status === 'failed')) {
    stopPolling()
    if (tasks.value.some((t) => t.status === 'done')) ElMessage.success('识别完成，可逐条确认录入')
  }
}

function stopPolling(): void {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

function removeTask(taskId: string): void {
  tasks.value = tasks.value.filter((t) => t.taskId !== taskId)
  if (activeTaskId.value === taskId) activeTaskId.value = null
}

onMounted(() => {
  document.addEventListener('paste', onPaste)
})

onBeforeUnmount(() => {
  document.removeEventListener('paste', onPaste)
  stopPolling()
})
</script>

<style scoped lang="scss">
.ocr-panel {
  &__drop {
    border: 2px dashed var(--border-1);
    border-radius: 12px;
    padding: 28px;
    text-align: center;
    cursor: pointer;
    color: var(--text-2);
    transition: border-color 0.2s, background 0.2s;

    &:hover,
    &--active {
      border-color: var(--accent);
      background: var(--accent-bg);
    }
  }

  &__title {
    margin: 8px 0 4px;
    font-size: 14px;
    color: var(--text-1);
  }

  &__list {
    margin-top: 12px;
    border: 1px solid var(--border-2);
    border-radius: 8px;
    overflow: hidden;
  }

  &__item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 12px;
    border-bottom: 1px solid var(--border-2);
    font-size: 13px;

    &:last-child {
      border-bottom: none;
    }
  }

  &__item-main {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  &__name {
    max-width: 240px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}
</style>
