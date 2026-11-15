import { get, put } from './client'
import type { MetaDTO, SettingsDTO } from './types'

export function fetchMeta(): Promise<MetaDTO> {
  return get<MetaDTO>('/meta')
}

export function fetchSettings(): Promise<SettingsDTO> {
  return get<SettingsDTO>('/settings')
}

export function updateSettings(patch: Partial<SettingsDTO>): Promise<SettingsDTO> {
  return put<SettingsDTO>('/settings', patch)
}
