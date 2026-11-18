import api from './client'

export interface SearchVideo {
  id: string | number
  title?: string
  description?: string
  cover_url?: string
  play_count?: number
  stats?: { play_count?: number }
}

export interface SearchUser {
  user_id?: string | number
  id?: string | number
  uid?: string | number
  username?: string
  nickname?: string
  avatar_url?: string
  bio?: string
}

export interface SearchTag {
  tag: string
  count: number
}

export interface SearchCategory<T> {
  status: 'ok' | 'unavailable'
  items: T[]
  total: number
  total_relation?: 'estimate' | 'eq' | 'gte'
  has_more: boolean
}

export interface SearchCategories {
  video?: SearchCategory<SearchVideo>
  user?: SearchCategory<SearchUser>
  tag?: SearchCategory<SearchTag>
}

export interface SearchResponse {
  schema_version?: number
  partial?: boolean
  categories?: SearchCategories
  total?: number
  total_relation?: 'estimate' | 'eq' | 'gte'
  next_cursor?: string
  has_more?: boolean
  scan_limit_reached?: boolean
  videos?: SearchVideo[] | null
  users?: SearchUser[] | null
  tags?: SearchTag[] | null
}

export const search = (q: string, type = 'all', page = 1, size = 20, signal?: AbortSignal, cursor?: string) =>
  api.get<{ data: SearchResponse }>('/v1/search', { params: { q, type, page, size, ...(cursor ? { candidate_cursor: cursor } : {}) }, signal })

export const suggest = (prefix: string, topN = 8, signal?: AbortSignal) =>
  api.get('/v1/search/suggest', { params: { prefix, top_n: topN }, signal })

export const searchUsers = (q: string, page = 1, size = 20, signal?: AbortSignal) =>
  api.get('/search/users', { params: { query: q, page, size }, signal })

export const hotWords = () =>
  api.get('/v1/search/hot')
