import { get, post, put, del } from './client'
import type { Paged, TagDTO } from './types'

export function listTags(q = '', includeArchived = false): Promise<Paged<TagDTO>> {
  return get<Paged<TagDTO>>('/tags', { q, include_archived: includeArchived })
}

export function createTag(payload: { name: string; color?: string }): Promise<TagDTO> {
  return post<TagDTO>('/tags', payload)
}

export function updateTag(id: string, payload: Partial<TagDTO>): Promise<TagDTO> {
  return put<TagDTO>(`/tags/${id}`, payload)
}

export function deleteTag(id: string): Promise<void> {
  return del(`/tags/${id}`)
}
