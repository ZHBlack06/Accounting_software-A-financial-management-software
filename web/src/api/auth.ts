import { post, get } from './client'

export interface LoginResult {
  ok: boolean
  version: string
}

export function login(token: string): Promise<LoginResult> {
  return post<LoginResult>('/auth/login', { token })
}

export function checkAuth(): Promise<{ ok: boolean; expires_in: number | null }> {
  return get('/auth/check')
}
