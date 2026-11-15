import { get, post, put, del } from './client'
import type { CategoryDTO, CategoryNode, TransactionType } from './types'

export function listCategories(type?: TransactionType, tree = false, includeArchived = false): Promise<{ items: CategoryNode[] }> {
  return get('/categories', { type, tree, include_archived: includeArchived })
}

export function createCategory(payload: Partial<CategoryDTO>): Promise<CategoryDTO> {
  return post<CategoryDTO>('/categories', payload)
}

export function updateCategory(id: string, payload: Partial<CategoryDTO>): Promise<CategoryDTO> {
  return put<CategoryDTO>(`/categories/${id}`, payload)
}

export function deleteCategory(id: string): Promise<void> {
  return del(`/categories/${id}`)
}
