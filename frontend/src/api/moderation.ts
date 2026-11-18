import api from './client'

export const getReports = (page = 1, size = 20) =>
  api.get('/v1/admin/reports', { params: { page, size } })

export const banUser = (uid: string, banned: boolean) =>
  api.put(`/v1/admin/users/${uid}/ban`, { banned })

export const muteUser = (uid: string, muted: boolean) =>
  api.put(`/v1/admin/users/${uid}/mute`, { muted })

export const reviewVideo = (videoId: string, status: string) =>
  api.put(`/v1/videos/${videoId}/review`, { status })

export const restoreComment = (commentId: string) =>
  api.put(`/v1/admin/comments/${commentId}/restore`)
