import api from './client'

export const getProfile = () =>
  api.get('/profile')

export const updateProfile = (data: { nickname?: string; bio?: string; avatar_url?: string }) =>
  api.put('/profile', {
    ...(data.nickname !== undefined ? { username: data.nickname } : {}),
    ...(data.bio !== undefined ? { bio: data.bio } : {}),
    ...(data.avatar_url !== undefined ? { avatar: data.avatar_url } : {}),
  })
