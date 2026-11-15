import { get, post, put, del } from './client'
import type { AccountDTO, AccountListDTO, AccountMergeReq, AccountMergeResult } from './types'

export function listAccounts(includeArchived = false, withBalance = true): Promise<AccountListDTO> {
  return get<AccountListDTO>('/accounts', {
    include_archived: includeArchived,
    with_balance: withBalance,
  })
}

export function createAccount(payload: Partial<AccountDTO>): Promise<AccountDTO> {
  return post<AccountDTO>('/accounts', payload)
}

export function updateAccount(id: string, payload: Partial<AccountDTO>): Promise<AccountDTO> {
  return put<AccountDTO>(`/accounts/${id}`, payload)
}

export function archiveAccount(id: string, archived: boolean): Promise<AccountDTO> {
  return post<AccountDTO>(`/accounts/${id}/archive`, { archived })
}

export function deleteAccount(id: string): Promise<void> {
  return del(`/accounts/${id}`)
}

export function reorderAccounts(items: { id: string; sort_order: number }[]): Promise<void> {
  return post('/accounts/reorder', { items })
}

/** 合并账户到「总账户」并全量重算（v1.2 §2.5 / R5）。 */
export function mergeAccounts(payload: AccountMergeReq): Promise<AccountMergeResult> {
  return post<AccountMergeResult>('/accounts/merge', payload, { timeout: 120000 })
}
