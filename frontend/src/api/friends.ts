import api from './client'

export const getFriends = (page = 1, size = 20) =>
  api.get('/friends', { params: { offset: (page - 1) * size, limit: size } })

export const getCommonFollowing = (uid: number) =>
  api.get('/common-following', { params: { target_id: uid } })
