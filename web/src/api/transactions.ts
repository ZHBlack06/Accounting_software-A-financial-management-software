import { get, post, put, del } from './client'
import type {
  FreezeConfirmReq,
  FreezeConfirmResult,
  AdjustPayload,
  AdjustResult,
  TransactionCreatePayload,
  TransactionCreateResult,
  TransactionDetailDTO,
  TransactionListDTO,
  TransactionQuery,
  TransactionUpdatePayload,
} from './types'

export function listTransactions(query: TransactionQuery): Promise<TransactionListDTO> {
  return get<TransactionListDTO>('/transactions', { ...query })
}

export function getTransaction(id: string): Promise<TransactionDetailDTO> {
  return get<TransactionDetailDTO>(`/transactions/${id}`)
}

export function createTransaction(payload: TransactionCreatePayload): Promise<TransactionCreateResult> {
  return post<TransactionCreateResult>('/transactions', payload)
}

export function updateTransaction(id: string, payload: TransactionUpdatePayload): Promise<{ transaction: import('./types').TransactionDTO }> {
  return put(`/transactions/${id}`, payload)
}

export function softDeleteTransaction(id: string, withTransfer = true): Promise<void> {
  return del(`/transactions/${id}`, { with_transfer: withTransfer })
}

export function restoreTransaction(id: string): Promise<void> {
  return post(`/transactions/${id}/restore`)
}

export function purgeTransaction(id: string): Promise<void> {
  return del(`/transactions/${id}/purge`)
}

export function adjustTransaction(id: string, payload: AdjustPayload): Promise<AdjustResult> {
  return post<AdjustResult>(`/transactions/${id}/adjust`, payload)
}

export function batchDeleteTransactions(ids: string[]): Promise<{ succeeded: string[]; failed: { id: string; code: string }[] }> {
  return post('/transactions/batch-delete', { ids })
}

/**
 * 确认冻结（v1.2 R6 / §9.6 端点 75）：逐笔 `transaction_ids` / 整批 `batch_id` / 全部 `all` 三选一。
 * 该端点会**同步**物化（后端实测约 1.3s/6746 行），超时按 10s 处理以免误判失败。
 */
export function confirmFreeze(payload: FreezeConfirmReq): Promise<FreezeConfirmResult> {
  return post<FreezeConfirmResult>('/transactions/freeze-confirm', payload, { timeout: 10000 })
}
