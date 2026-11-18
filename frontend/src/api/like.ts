import api from './client'

export const likeVideo = (videoId: string) =>
  api.post('/v1/likes', { target_id: videoId, target_type: 'video' })

export const unlikeVideo = (videoId: string) =>
  api.delete('/v1/likes', { data: { target_id: videoId, target_type: 'video' } })

export const checkLiked = (videoId: string) =>
  api.get('/v1/likes/check', { params: { target_id: videoId, target_type: 'video' } })

export const getLikeCount = (videoId: string) =>
  api.get('/v1/likes/count', { params: { target_id: videoId, target_type: 'video' } })
