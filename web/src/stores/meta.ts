import { defineStore } from 'pinia'
import { fetchMeta, updateSettings } from '@/api/meta'
import type { MetaDTO, TimePrecision } from '@/api/types'
import { DEFAULT_TIME_PRECISION } from '@/utils/datetime'

interface MetaState {
  /** 是否已成功拉取过 /api/meta。 */
  loaded: boolean
  version: string
  /** 当前后端发布版本（meta.app_version ?? meta.version），用于界面显示。 */
  appVersion: string
  schemaVersion: number
  freezeDays: number
  freezeBoundary: string
  timezone: string
  defaultAccountId: string | null
  ocrAvailable: boolean
  /** 记录时间精度（v1.2 R2）：切到 day 后全站时间展示立即收敛到日。 */
  timePrecision: TimePrecision
  needsRematerialize: boolean
}

/**
 * 全局 meta 状态（v1.2 T07）：承载 settings/meta 里的 `time_precision`。
 * 组件通过 `useMetaStore().precision` 拿当前精度，避免每处各自请求。
 */
export const useMetaStore = defineStore('meta', {
  state: (): MetaState => ({
    loaded: false,
    version: '',
    appVersion: '',
    schemaVersion: 1,
    freezeDays: 30,
    freezeBoundary: '',
    timezone: 'Asia/Shanghai',
    defaultAccountId: null,
    ocrAvailable: false,
    timePrecision: DEFAULT_TIME_PRECISION,
    needsRematerialize: false,
  }),
  getters: {
    /** 当前生效的时间精度（模板里直接 `meta.precision`）。 */
    precision(state): TimePrecision {
      return state.timePrecision
    },
    isDayPrecision(state): boolean {
      return state.timePrecision === 'day'
    },
  },
  actions: {
    /** 应用 meta DTO 到 store（登录后由 AppLayout 调用一次）。 */
    applyDto(dto: MetaDTO): void {
      this.version = dto.version ?? ''
      this.appVersion = dto.app_version || dto.version || ''
      this.schemaVersion = dto.schema_version ?? 1
      this.freezeDays = dto.freeze_days ?? 30
      this.freezeBoundary = dto.freeze_boundary ?? ''
      this.timezone = dto.timezone ?? 'Asia/Shanghai'
      this.defaultAccountId = dto.default_account_id ?? null
      this.ocrAvailable = Boolean(dto.ocr_available)
      this.needsRematerialize = Boolean(dto.needs_rematerialize)
      this.timePrecision = normalizePrecision(dto.time_precision)
      this.loaded = true
    },
    async load(): Promise<void> {
      const dto = await fetchMeta()
      this.applyDto(dto)
    },
    /** 切换精度：先落库（PUT /settings）再更新本地，失败抛给调用方提示。 */
    async setTimePrecision(p: TimePrecision): Promise<void> {
      const res = await updateSettings({ time_precision: p })
      this.timePrecision = normalizePrecision(res?.time_precision ?? p)
    },
    /** 不落库的本地覆盖（测试 / 离线兜底用）。 */
    setLocalPrecision(p: TimePrecision): void {
      this.timePrecision = normalizePrecision(p)
    },
  },
})

/** 后端字段可能是 undefined / 脏值，统一收敛到合法枚举。 */
export function normalizePrecision(v: unknown): TimePrecision {
  return v === 'day' ? 'day' : DEFAULT_TIME_PRECISION
}
