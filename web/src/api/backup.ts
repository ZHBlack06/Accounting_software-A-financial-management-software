import { del, get, post } from './client'
import type { BackupCreateResult, BackupItem, BackupRestoreResult } from './types'

export function createBackup(): Promise<BackupCreateResult> {
  return post<BackupCreateResult>('/backup/create')
}

export function listBackups(): Promise<{ items: BackupItem[] }> {
  return get('/backup/list')
}

export function restoreBackup(file: string): Promise<BackupRestoreResult> {
  return post<BackupRestoreResult>('/backup/restore', { file, confirm: true })
}

export function deleteBackup(file: string): Promise<void> {
  return del(`/backup/${encodeURIComponent(file)}`)
}
