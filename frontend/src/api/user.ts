import axios from 'axios'
import api from './client'

// Shared client supplies CSRF and refresh coordination; proxies map these aliases.
const authAxios = api

export const follow = (targetId: number) =>
  authAxios.post('/follow', { target_id: targetId })

export const unfollow = (targetId: number) =>
  authAxios.post('/unfollow', { target_id: targetId })

export const isFollowing = (targetId: number) =>
  authAxios.get('/is-following', { params: { target_id: targetId } })

export const getFollowers = (userId: number, offset = 0, limit = 20) =>
  authAxios.get('/followers', { params: { user_id: userId, offset, limit } })

export const getFollowing = (userId: number, offset = 0, limit = 20) =>
  authAxios.get('/following', { params: { user_id: userId, offset, limit } })

// Public endpoints for target user's relations (no auth required)
export const getUserFollowers = (uid: number, offset = 0, limit = 20) =>
  axios.get(`/api/v1/users/${uid}/followers`, { params: { offset, limit } })

export const getUserFollowing = (uid: number, offset = 0, limit = 20) =>
  axios.get(`/api/v1/users/${uid}/following`, { params: { offset, limit } })
