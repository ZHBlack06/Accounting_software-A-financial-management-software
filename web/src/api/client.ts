/**
 * axios 实例：token 注入、统一错误体解析、401 跳登录、VITE_MOCK 开关。
 * 错误码文案表见 ARCHITECTURE §10.1。
 */
import axios, { AxiosError, type AxiosInstance, type AxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'
import type { ApiErrorBody } from './types'
import { mockAdapter } from './mock'

/** Mock 开关：默认 false，交付前必须为 false。后端未就绪时可用 VITE_MOCK=true 开发。 */
export const MOCK: boolean = import.meta.env.VITE_MOCK === 'true'

/** 结构化 API 错误，供业务层按 code 分支处理（如 FROZEN 自动弹调整框）。 */
export class ApiError extends Error {
  readonly code: string
  readonly status: number
  readonly details: Record<string, unknown>

  constructor(status: number, code: string, message: string, details: Record<string, unknown> = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }
}

/** §10.1 错误码 → 中文默认文案（后端 message 优先，此表兜底）。 */
const ERROR_TEXT: Record<string, string> = {
  VALIDATION_ERROR: '输入校验失败，请检查表单',
  UNAUTHORIZED: '登录已失效，请重新登录',
  BAD_TOKEN: '访问令牌错误',
  NOT_FOUND: '资源不存在或已被删除',
  FROZEN: '该交易已进入冻结区，不可直接修改，请使用调整分录',
  DUPLICATE: '检测到疑似重复记录',
  IN_USE: '该记录被引用，不能删除，建议归档',
  ACCOUNT_IN_USE: '该账户已有交易，不能删除，建议归档',
  CATEGORY_IN_USE: '该分类被引用，不能删除，建议归档',
  TRANSFER_IMMUTABLE: '转账金额与账户不可直接修改，请使用调整',
  NO_EFFECT: '调整后金额不变，无需调整',
  RETENTION: '删除未满 180 天，不能彻底清除',
  SEQUENCE_OVERFLOW: '系统序列耗尽，请联系维护人员',
  MATERIALIZE_FAILED: '余额物化失败，请暂停写操作并联系维护',
  OCR_UNAVAILABLE: 'OCR 服务未启动，可手动录入或稍后重试',
  OCR_TASK_NOT_FOUND: '识别任务已过期，请重新上传',
  UNSUPPORTED_CSV: '无法识别账单格式，请手动选择来源',
  IMPORT_BATCH_EXPIRED: '导入批次已超过 24 小时，请重新上传',
  RESTORING: '备份恢复中，请稍候',
  BACKUP_FAILED: '备份/恢复失败，请查看日志',
  INTERNAL: '服务器内部错误，请稍后重试',
}

export function errorText(err: unknown): string {
  if (err instanceof ApiError) {
    return err.message || ERROR_TEXT[err.code] || '请求失败'
  }
  if (err instanceof Error) return err.message
  return '请求失败'
}

export const client: AxiosInstance = axios.create({
  baseURL: '/api',
  timeout: 30000,
  headers: { 'Content-Type': 'application/json' },
})

client.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

/** 由 AppLayout 注册：收到 FROZEN 等需要 UI 引导的错误时回调（避免 store 循环依赖）。 */
let frozenGuideHandler: ((err: ApiError) => void) | null = null
export function onFrozenError(handler: (err: ApiError) => void): void {
  frozenGuideHandler = handler
}

client.interceptors.response.use(
  (resp) => resp,
  (error: AxiosError<ApiErrorBody>) => {
    const status = error.response?.status ?? 0
    const body = error.response?.data
    const code = body?.error?.code ?? (status === 0 ? 'NETWORK' : 'INTERNAL')
    const message = body?.error?.message || ERROR_TEXT[code] || error.message || '网络异常'
    const details = body?.error?.details ?? {}
    const apiErr = new ApiError(status, code, message, details)

    if (status === 401) {
      localStorage.removeItem('token')
      if (window.location.pathname !== '/login') {
        ElMessage.error(errorText(apiErr))
        window.location.href = '/login'
      }
      return Promise.reject(apiErr)
    }
    if (code === 'FROZEN' && frozenGuideHandler) {
      frozenGuideHandler(apiErr)
    }
    return Promise.reject(apiErr)
  },
)

if (MOCK) {
  client.defaults.adapter = mockAdapter
}

/** 通用 GET，query 中数组转逗号分隔（契约 §5.4 支持 csv），undefined 不发送。 */
export async function get<T>(url: string, params?: Record<string, unknown>): Promise<T> {
  const resp = await client.get<T>(url, { params: serializeParams(params) })
  return resp.data
}

export async function post<T>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T> {
  const resp = await client.post<T>(url, data, config)
  return resp.data
}

export async function put<T>(url: string, data?: unknown): Promise<T> {
  const resp = await client.put<T>(url, data)
  return resp.data
}

export async function del<T>(url: string, params?: Record<string, unknown>): Promise<T> {
  const resp = await client.delete<T>(url, { params: serializeParams(params) })
  return resp.data
}

function serializeParams(params?: Record<string, unknown>): Record<string, unknown> | undefined {
  if (!params) return undefined
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue
    out[k] = Array.isArray(v) ? v.join(',') : v
  }
  return out
}
