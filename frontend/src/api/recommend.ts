import api from './client'

export const getForYou = (params?: { size?: number; cursor?: string }) =>
  api.get('/v1/recommend/for-you', { params })

export const getSimilar = (videoId: string, params?: { size?: number }) =>
  api.get(`/v1/recommend/similar/${videoId}`, { params })
