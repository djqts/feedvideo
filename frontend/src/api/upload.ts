import api, { uploadApi } from './client'

export interface UploadOptions {
  onProgress?: (e: { loaded: number; total?: number }) => void
  signal?: AbortSignal
}

export interface UploadResult {
  video_url: string
  cover_url: string
  upload_id?: string
  content_version?: string | number
  lease_expires_at?: string
}

export async function getUploadReference(id: string, signal: AbortSignal): Promise<UploadResult & { status: string }> {
  const { data } = await api.get(`/v1/uploads/${id}`, { signal, skipAuthRefresh: true, timeout: 10000 })
  return data.data
}

export async function uploadFiles(video: File, cover?: File, options?: UploadOptions): Promise<UploadResult> {
  const form = new FormData()
  form.append('video', video)
  if (cover) form.append('cover', cover)
  const { data } = await uploadApi.post('/v1/upload', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
    onUploadProgress: (progressEvent) => {
      if (options?.onProgress && progressEvent.total != null) {
        options.onProgress({ loaded: progressEvent.loaded, total: progressEvent.total })
      }
    },
    signal: options?.signal,
  })
  return data.data as UploadResult
}
