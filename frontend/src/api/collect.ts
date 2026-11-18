import api from './client'

export const collect = (videoId: string) =>
  api.post('/v1/collect', { video_id: videoId })

export const getCollectedVideos = (page = 1, size = 10) =>
  api.get('/v1/collect/list', { params: { page, size } })

export const checkCollected = (videoId: string) =>
  api.get('/v1/collect/check', { params: { video_id: videoId } })

export const setCollected = (videoId: string, collected: boolean, version?: string) => {
  const body = { video_id: videoId, collected }
  return version === undefined
    ? api.put('/v1/collect', body)
    : api.put('/v1/collect', body, { headers: { 'If-Match': `"${version}"` } })
}
