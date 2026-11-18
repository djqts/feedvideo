import api from './client'

/** 行为统计（管理员可传 scope=platform 查看全平台聚合） */
export const getBehaviorStats = (days = 7, scope: '' | 'platform' = 'platform') =>
  api.get('/v1/behavior/stats', { params: { days, scope } })

/** 死信队列重放（管理员） */
export const replayDlq = (count = 100) =>
  api.post('/v1/admin/notifications/dlq/replay', null, { params: { count } })

/** 系统通知广播（管理员；all=true 广播全部用户） */
export const postSystemNotification = (payload: {
  content: string
  all?: boolean
  recipient_ids?: string[]
}) => api.post('/v1/notifications/system', payload)

/** 待审核视频列表（管理员） */
export const getAdminVideos = (
  status: 'pending' | 'rejected' | 'approved' = 'pending',
  page = 1,
  pageSize = 20,
) => api.get('/v1/admin/videos', { params: { status, page, page_size: pageSize } })

/** 重放失败 outbox 事件（管理员） */
export const replayOutbox = (limit = 100) =>
  api.post('/v1/admin/outbox/replay', { limit })

/** 重放媒体死信（管理员，legacy 模式） */
export const replayMediaDlq = (limit = 50) =>
  api.post('/v1/admin/media/dlq/replay', { limit })

/** 删除通知（管理员） */
export const deleteNotifications = (ids: string[]) =>
  api.delete('/v1/admin/notifications', { data: { ids } })

/** 批量同步视频统计（管理员，最多 100 个 ID） */
export const syncVideoStats = (videoIds: string[]) =>
  api.post('/v1/videos/sync-stats', { video_ids: videoIds })
