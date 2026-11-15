import { get, post } from './client'
import type { HealthDTO } from './types'

export function fetchHealth(): Promise<HealthDTO> {
  // health 免认证且不带 /api 前缀差异，仍走同一实例
  return get<HealthDTO>('/health')
}

export function verifyBalances(): Promise<{ ok: boolean; checked_rows: number; mismatches: unknown[] }> {
  return get('/maintenance/verify')
}

export function rematerialize(full = true): Promise<{ rows: number; seconds: number }> {
  return post('/maintenance/rematerialize', { full })
}
