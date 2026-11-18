import api from './client'

export const getComments = (videoId: string, page = 1, size = 20) =>
  api.get(`/v1/videos/${videoId}/comments`, { params: { page, size } })

export const postComment = (videoId: string, content: string, parentId?: string) =>
  api.post(`/v1/videos/${videoId}/comments`, {
    content,
    ...(parentId ? { parent_id: parentId } : {}),
  })

export const likeComment = (commentId: string) =>
  api.post(`/v1/comments/${commentId}/like`)

export const unlikeComment = (commentId: string) =>
  api.delete(`/v1/comments/${commentId}/like`)

export const reportComment = (commentId: string, reason: string) =>
  api.post(`/v1/comments/${commentId}/report`, { reason })

export const deleteComment = (commentId: string) =>
  api.delete(`/v1/comments/${commentId}`)
