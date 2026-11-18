import React, { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import api from '../api/client'
import PageHeader from '../components/PageHeader'
import { Button } from '../components/ui/button'
import ErrorState from '../components/ErrorState'
import { ListSkeleton } from '../components/skeletons/ListSkeleton'
import { classifyError, type ClassifiedError } from '../lib/classifyError'

interface WatchEvent {
  event_id?: string
  type?: string
  occurred_at?: string
  payload?: Record<string, any>
}

const typeLabels: Record<string, string> = {
  play: '播放',
  pause: '暂停',
  seek: '拖拽',
  complete: '看完',
  heartbeat: '观看中',
  like: '点赞',
  collect: '收藏',
  share: '分享',
}

/** 观看历史：读取后端 GET /v1/behavior/events 的事件流。 */
export default function HistoryPage() {
  const [events, setEvents] = useState<WatchEvent[]>([])
  const [cursor, setCursor] = useState('')
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<ClassifiedError | null>(null)

  const load = useCallback(async (nextCursor = '') => {
    if (nextCursor) setLoadingMore(true)
    else setLoading(true)
    setError(null)
    try {
      const { data } = await api.get('/v1/behavior/events', {
        params: { limit: 50, ...(nextCursor ? { cursor: nextCursor } : {}) },
      })
      const payload = data?.data || {}
      const list: WatchEvent[] = Array.isArray(payload.list) ? payload.list : []
      setEvents((prev) => (nextCursor ? [...prev, ...list] : list))
      setHasMore(Boolean(payload.has_more))
      setCursor(payload.next_cursor || '')
    } catch (err) {
      setError(classifyError(err))
    } finally {
      setLoading(false)
      setLoadingMore(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const videoIdOf = (event: WatchEvent): string => {
    const payload = event.payload || {}
    const raw = payload.video_id ?? payload.target_id ?? payload.id
    return typeof raw === 'string' ? raw : ''
  }

  return (
    <div className="mx-auto max-w-4xl">
      <PageHeader eyebrow="HISTORY" title="观看历史" description="最近的播放与观看事件，按时间倒序。" />
      {loading ? (
        <ListSkeleton n={6} />
      ) : error ? (
        <ErrorState error={error} onRetry={() => { void load() }} />
      ) : events.length === 0 ? (
        <p className="ef-empty">还没有观看记录，去首页看看视频吧。</p>
      ) : (
        <ul className="border-t border-border">
          {events.map((event, index) => {
            const videoId = videoIdOf(event)
            return (
              <li key={event.event_id || `${event.type}-${event.occurred_at}-${index}`} className="ef-record flex flex-wrap items-center justify-between gap-3 border-b border-border py-4">
                <div className="min-w-0">
                  <p className="text-sm font-medium">
                    {typeLabels[event.type || ''] || event.type || '事件'}
                    {videoId ? ' · ' : ''}
                    {videoId && <Link to={`/videos/${videoId}`} className="text-primary hover:underline">视频 {videoId}</Link>}
                  </p>
                  <p className="ef-number text-xs text-muted-foreground mt-1">
                    {event.occurred_at ? new Date(event.occurred_at).toLocaleString() : '时间未知'}
                  </p>
                </div>
                {videoId && <Link to={`/videos/${videoId}`} className="ef-cut border border-border px-4 py-2 text-sm hover:bg-muted">去观看</Link>}
              </li>
            )
          })}
        </ul>
      )}
      {hasMore && (
        <div className="mt-5 text-center">
          <Button variant="outline" disabled={loadingMore} onClick={() => { void load(cursor) }}>
            {loadingMore ? '加载中…' : '加载更多'}
          </Button>
        </div>
      )}
    </div>
  )
}
