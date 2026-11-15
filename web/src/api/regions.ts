import { get } from './client'
import type { Paged, RegionDTO } from './types'

export function searchRegions(q: string, limit = 20): Promise<Paged<RegionDTO>> {
  return get<Paged<RegionDTO>>('/regions/search', { q, limit })
}

export function getRegion(code: string): Promise<RegionDTO> {
  return get<RegionDTO>(`/regions/${code}`)
}
