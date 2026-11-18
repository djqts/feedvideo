import api from './client'

export const createTopic = (name: string, description?: string) =>
  api.post('/v1/topics', { name, description })

export const getTopic = (name: string) => api.get(`/v1/topics/${name}`)

export const getTopicByID = (topicId: string) => api.get(`/v1/topics/id/${topicId}`)

export const getHotTopics = () => api.get('/v1/topics/hot')
