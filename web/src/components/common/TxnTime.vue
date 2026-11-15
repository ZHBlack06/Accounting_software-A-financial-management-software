<template>
  <el-tooltip :disabled="!isDistributed" :content="tip" placement="top">
    <span class="txn-time" :class="{ 'txn-time--mono': mono }">
      <span class="txn-time__text">{{ text }}</span>
      <span v-if="isDistributed" class="txn-time__dot" aria-hidden="true" />
    </span>
  </el-tooltip>
</template>

<script setup lang="ts">
/**
 * 统一的时间展示单元（v1.2 T07 / R2「两层精度」规则）。
 *
 * - 全局 `settings.time_precision` 只是**默认展示偏好**；
 * - 行级 `transactions.time_precision` 是**事实**：`day` 表示该行时分秒由系统按当日平均分布，
 *   `second` 表示用户明确录入过时间；
 * - 因此**行级优先**：行级为 `day` 时即使全局是 `second` 也只显示 `YYYY-MM-DD`，
 *   并在右侧加一个小圆点 + tooltip 说明「时分秒由系统分布，非用户录入」，绝不把分布出来的时刻
 *   伪装成用户选的精确时间；同时**只做展示降级，不回写 00:00:00**。
 * - `tabular-nums` 保证 10 字符 / 19 字符两种宽度在同一列里不抖动。
 */
import { computed } from 'vue'
import { fmtFullTxnTime, fmtTxnTime } from '@/utils/datetime'
import { useMetaStore } from '@/stores/meta'
import type { TimePrecision } from '@/api/types'

const props = withDefaults(
  defineProps<{
    /** 'YYYY-MM-DD HH:mm:ss' */
    time: string
    /** 行级精度（缺省 = 无行级信息，用全局）。 */
    rowPrecision?: TimePrecision | null
    /** true = 详情场景（second 下显示完整秒）。 */
    full?: boolean
    /** 等宽数字（列表列内推荐开启）。 */
    mono?: boolean
  }>(),
  { rowPrecision: null, full: false, mono: true },
)

const meta = useMetaStore()

/** 行级优先，缺行级信息时才用全局偏好。 */
const precision = computed<TimePrecision>(() =>
  props.rowPrecision === 'day' || props.rowPrecision === 'second' ? props.rowPrecision : meta.precision,
)

const isDistributed = computed(() => props.rowPrecision === 'day')

const text = computed(() =>
  props.full ? fmtFullTxnTime(props.time, precision.value) : fmtTxnTime(props.time, precision.value),
)

const tip = computed(() =>
  `源数据无时间，时分秒由系统按当日顺序分布（实际值 ${props.time}），非用户录入的精确时间`,
)
</script>

<style scoped lang="scss">
.txn-time {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;

  &--mono {
    font-variant-numeric: tabular-nums;
  }

  &__text {
    font-variant-numeric: tabular-nums;
  }

  &__dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: var(--frozen);
    flex: 0 0 auto;
    cursor: help;
  }
}
</style>
