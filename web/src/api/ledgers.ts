import { get, post, put, del } from './client'
import type { LedgerDTO, LedgerRule, Paged } from './types'

export function listLedgers(includeArchived = false, withStats = true): Promise<Paged<LedgerDTO>> {
  return get<Paged<LedgerDTO>>('/ledgers', { include_archived: includeArchived, with_stats: withStats })
}

export function createLedger(payload: Partial<LedgerDTO>): Promise<LedgerDTO> {
  return post<LedgerDTO>('/ledgers', payload)
}

export function updateLedger(id: string, payload: Partial<LedgerDTO>): Promise<LedgerDTO> {
  return put<LedgerDTO>(`/ledgers/${id}`, payload)
}

export function archiveLedger(id: string, archived: boolean): Promise<LedgerDTO> {
  return post<LedgerDTO>(`/ledgers/${id}/archive`, { archived })
}

export function deleteLedger(id: string): Promise<void> {
  return del(`/ledgers/${id}`)
}

export function getLedgerRules(id: string): Promise<{ rules: LedgerRule[] }> {
  return get(`/ledgers/${id}/rules`)
}

export function putLedgerRules(id: string, rules: Omit<LedgerRule, 'id'>[]): Promise<{ rules: LedgerRule[] }> {
  return put(`/ledgers/${id}/rules`, { rules })
}
