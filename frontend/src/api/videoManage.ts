import api from './client'

export const updateVideo = (id: string, data: { title?: string; description?: string; tags?: string[] }) =>
  api.put(`/v1/videos/${id}`, data)

export const deleteVideo = (id: string) =>
  api.delete(`/v1/videos/${id}`)
