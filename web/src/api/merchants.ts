import { get, post, put, del } from './client'
import type { MerchantDTO, Paged } from './types'

export function searchMerchants(q = '', type = '', limit = 20): Promise<Paged<MerchantDTO>> {
  return get<Paged<MerchantDTO>>('/merchants', { q, type, limit })
}

export function createMerchant(payload: Partial<MerchantDTO>): Promise<MerchantDTO> {
  return post<MerchantDTO>('/merchants', payload)
}

export function updateMerchant(id: string, payload: Partial<MerchantDTO>): Promise<MerchantDTO> {
  return put<MerchantDTO>(`/merchants/${id}`, payload)
}

export function deleteMerchant(id: string): Promise<void> {
  return del(`/merchants/${id}`)
}
