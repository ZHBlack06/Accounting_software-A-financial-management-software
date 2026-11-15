import { client, get, put } from './client'
import type { OcrModelDTO, OcrRecognizeAsyncResult, OcrTaskDTO } from './types'

/** OCR 识别（异步模式）：multipart 上传图片，返回 task_id 供轮询。 */
export async function recognizeAsync(file: File, model: 'auto' | 'light' | 'heavy' = 'auto'): Promise<OcrRecognizeAsyncResult> {
  const fd = new FormData()
  fd.append('file', file)
  fd.append('model', model)
  fd.append('mode', 'async')
  const resp = await client.post<OcrRecognizeAsyncResult>('/ocr/recognize', fd, {
    headers: { 'Content-Type': 'multipart/form-data' },
    timeout: 60000,
  })
  return resp.data
}

export function getOcrTask(taskId: string): Promise<OcrTaskDTO> {
  return get<OcrTaskDTO>(`/ocr/tasks/${taskId}`)
}

export function getOcrModel(): Promise<OcrModelDTO> {
  return get<OcrModelDTO>('/ocr/model')
}

export function switchOcrModel(model: string): Promise<{ ok: boolean; current?: string; error?: string }> {
  return put('/ocr/model', { model })
}

/**
 * 截图预览：带认证头拉二进制 → object URL（不直接暴露本地路径）。
 * 调用方负责 URL.revokeObjectURL。
 */
export async function fetchScreenshotUrl(screenshotId: string, size: 'compressed' | 'original' = 'compressed'): Promise<string> {
  const resp = await client.get<Blob>(`/screenshots/${screenshotId}/file`, {
    params: { size },
    responseType: 'blob',
  })
  return URL.createObjectURL(resp.data)
}
