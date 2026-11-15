<template>
  <div>
    <div class="panel-toolbar">
      <el-button type="primary" size="small" :loading="creating" @click="create">立即创建备份</el-button>
      <span class="text-tertiary">备份内容：ledger.db + manifest.json（含 sha256）；恢复会先自动创建 pre-restore 备份</span>
    </div>

    <el-table v-loading="loading" :data="items" size="small" empty-text="暂无备份">
      <el-table-column prop="file" label="备份文件" min-width="220" />
      <el-table-column label="大小" width="110" align="right">
        <template #default="{ row }">{{ formatSize(row.size_bytes) }}</template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="180" />
      <el-table-column label="校验" width="140">
        <template #default="{ row }">
          <span class="text-tertiary">{{ (row.checksum || '').slice(0, 14) }}…</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="170" fixed="right">
        <template #default="{ row }">
          <el-button link size="small" type="warning" @click="restore(row)">恢复</el-button>
          <el-button link size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-alert
      type="info"
      :closable="false"
      style="margin-top: 12px"
      title="备份策略"
      description="每周一自动备份；截图原件保留 30 天、压缩图 150 天、备份 150 天（可在后端维护任务中调整）。"
    />
  </div>
</template>

<script setup lang="ts">
/** 备份设置（T10 验收 9）：创建 / 列表 / 恢复（输入 RESTORE 二次确认 + 全屏 loading）/ 删除。 */
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import { createBackup, deleteBackup, listBackups, restoreBackup } from '@/api/backup'
import { errorText } from '@/api/client'
import type { BackupItem } from '@/api/types'

const loading = ref(false)
const creating = ref(false)
const items = ref<BackupItem[]>([])

function formatSize(bytes: number): string {
  if (bytes > 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  if (bytes > 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${bytes} B`
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await listBackups()
    items.value = res.items
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    loading.value = false
  }
}

async function create(): Promise<void> {
  creating.value = true
  const inst = ElLoading.service({ fullscreen: true, text: '正在备份（WAL checkpoint + 压缩）…' })
  try {
    const res = await createBackup()
    ElMessage.success(`备份完成：${res.file}（${formatSize(res.size_bytes)}）`)
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    inst.close()
    creating.value = false
  }
}

async function restore(row: BackupItem): Promise<void> {
  let input = ''
  try {
    const result = await ElMessageBox.prompt(
      `恢复将覆盖当前全部数据，请输入 RESTORE 确认：`,
      `恢复到 ${row.file}`,
      {
        confirmButtonText: '确认恢复',
        cancelButtonText: '取消',
        inputPlaceholder: 'RESTORE',
        inputValidator: (v: string) => (v === 'RESTORE' ? true : '请输入大写 RESTORE'),
      },
    )
    input = result.value
  } catch {
    return
  }
  if (input !== 'RESTORE') return
  const inst = ElLoading.service({ fullscreen: true, text: '正在恢复数据并校验余额，请勿关闭页面…' })
  try {
    const res = await restoreBackup(row.file)
    ElMessage.success(`恢复完成，校验${res.verify.ok ? '通过' : '异常'}（${res.verify.checked_rows} 行）`)
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    inst.close()
  }
}

async function remove(row: BackupItem): Promise<void> {
  try {
    await ElMessageBox.confirm(`确定删除备份「${row.file}」？`, '删除备份', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteBackup(row.file)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}

onMounted(load)
</script>

<style scoped lang="scss">
.panel-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}
</style>
