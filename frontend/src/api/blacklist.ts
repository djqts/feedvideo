import api from './client'

export const blackUser = (uid: number) =>
  api.post('/black', { target_id: uid })

export const unblackUser = (uid: number) =>
  api.post('/unblack', { target_id: uid })

export const getBlacklist = (page = 1, size = 20) =>
  api.get('/blacklist', { params: { offset: (page - 1) * size, limit: size } })
