<template>
  <el-select
    :model-value="modelValue"
    multiple
    filterable
    allow-create
    default-first-option
    :reserve-keyword="false"
    placeholder="选择或输入新标签"
    style="width: 100%"
    :loading="creating"
    @update:model-value="onUpdate"
    @change="onChange"
  >
    <el-option v-for="t in master.tags" :key="t.id" :value="t.id" :label="t.name">
      <span :style="{ color: t.color }">●</span> {{ t.name }}
    </el-option>
  </el-select>
</template>

<script setup lang="ts">
/** 标签多选 + 即时新建（输入不存在的名字时创建新标签）。 */
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { createTag } from '@/api/tags'
import { useMasterStore } from '@/stores/master'
import { errorText } from '@/api/client'

defineProps<{ modelValue: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [v: string[]] }>()

const master = useMasterStore()
const creating = ref(false)
const pendingNames = ref<string[]>([])

function onUpdate(v: (string | number)[]): void {
  emit('update:modelValue', v.map(String))
}

/** el-select allow-create 会把新名字直接塞进值里，change 时把非 id 的值替换为新建标签 id。 */
async function onChange(values: string[]): Promise<void> {
  const knownIds = new Set(master.tags.map((t) => t.id))
  const resolved: string[] = []
  for (const v of values) {
    if (knownIds.has(v)) {
      resolved.push(v)
      continue
    }
    // 已存在同名标签则复用
    const existing = master.tags.find((t) => t.name === v)
    if (existing) {
      resolved.push(existing.id)
      continue
    }
    if (pendingNames.value.includes(v)) continue
    pendingNames.value.push(v)
    creating.value = true
    try {
      const created = await createTag({ name: v })
      master.tags.push(created)
      resolved.push(created.id)
    } catch (e) {
      ElMessage.error(`创建标签失败：${errorText(e)}`)
    } finally {
      pendingNames.value = pendingNames.value.filter((n) => n !== v)
      creating.value = false
    }
  }
  emit('update:modelValue', resolved)
}
</script>
