import api from './client'

export const share = (videoId: string) =>
  api.post(`/v1/share/${videoId}`)
