import { client, post } from './client'
import type {
  CsvCommitPayload,
  CsvCommitResult,
  CsvPreviewDTO,
  SheetCommitReq,
  SheetCommitResult,
  SheetPreviewDTO,
  SheetPreviewReq,
  SheetProbeDTO,
} from './types'

// ---------- 微信 / 支付宝 CSV（v1.0 §5.7，保持原样） ----------

/** CSV 预览：multipart 上传，source=auto|wechat|alipay。 */
export async function previewCsv(
  file: File,
  accountId?: string,
  source: 'auto' | 'wechat' | 'alipay' = 'auto',
): Promise<CsvPreviewDTO> {
  const fd = new FormData()
  fd.append('file', file)
  if (accountId) fd.append('account_id', accountId)
  fd.append('source', source)
  const resp = await client.post<CsvPreviewDTO>('/import/csv/preview', fd, {
    headers: { 'Content-Type': 'multipart/form-data' },
    timeout: 60000,
  })
  return resp.data
}

export function commitCsv(payload: CsvCommitPayload): Promise<CsvCommitResult> {
  return post<CsvCommitResult>('/import/csv/commit', payload)
}

// ---------- Excel / CSV 账本（v1.2 §2.2–§2.4） ----------

/**
 * 探测上传的表格文件（1..20 个，同一字段名 `files` 重复出现）。
 * 返回 sheet 列表 / 表头候选 / 样本行 / 建议映射 / 建议跳过。
 */
export async function probeSheets(files: File[], accountId?: string | null): Promise<SheetProbeDTO> {
  const fd = new FormData()
  for (const f of files) fd.append('files', f)
  if (accountId) fd.append('account_id', accountId)
  const resp = await client.post<SheetProbeDTO>('/import/sheet/probe', fd, {
    headers: { 'Content-Type': 'multipart/form-data' },
    // 6746 行 × 4 表的真实样本探测可能超过 30s，放宽到 120s。
    timeout: 120000,
  })
  return resp.data
}

/** 提交映射 → 逐行解析预览（rows 最多 200 行 + rows_truncated）。 */
export function previewSheets(payload: SheetPreviewReq): Promise<SheetPreviewDTO> {
  return post<SheetPreviewDTO>('/import/sheet/preview', payload, { timeout: 180000 })
}

/** 提交入库（幂等 + 分块事务）。 */
export function commitSheets(payload: SheetCommitReq): Promise<SheetCommitResult> {
  return post<SheetCommitResult>('/import/sheet/commit', payload, { timeout: 300000 })
}
