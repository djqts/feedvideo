import React, { useState, useEffect, useRef, useCallback } from 'react'
import PageHeader from '../components/PageHeader'
import api from '../api/client'
import { Button } from '../components/ui/button'
import { usePolling } from '../hooks/usePolling'
import { cn } from '../lib/utils'
import { toast } from 'sonner'
import { ListSkeleton } from '../components/skeletons/ListSkeleton'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'

const TABS = [
  { key: 'all', label: '全部' },
  { key: 'interaction', label: '互动' },
  { key: 'follow', label: '关注' },
  { key: 'system', label: '系统' },
] as const

type TabKey = (typeof TABS)[number]['key']

const typeLabel: Record<string, string> = {
  like: '赞', comment: '评', follow: '关', at: '@', interaction: '互', system: '系',
}

const typeName: Record<string, string> = {
  like: '点赞', comment: '评论', follow: '关注', at: '提及', interaction: '互动', system: '系统通知',
}

const defaultMessage: Record<string, string> = {
  like: '赞了你的内容', comment: '评论了你的内容', follow: '关注了你', at: '提到了你',
}

export default function NotificationPage() {
  const [list, setList] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)
  const [activeTab, setActiveTab] = useState<TabKey>('all')
  const [unreadCounts, setUnreadCounts] = useState<Record<string, number>>({})

  // Task 3.6: Mark-all-read state
  const [markingAllRead, setMarkingAllRead] = useState(false)
  const [markAllFailed, setMarkAllFailed] = useState(false)

  // Task 3.3: Mutation lock for polling race condition fix
  const mutationLock = useRef({ locked: false, version: 0 })
  const requestSequence = useRef(0)
  const unreadSequence = useRef(0)
  const requestPending = useRef(false)
  const pollPending = useRef(false)
  const currentTab = useRef<TabKey>('all')
  const mounted = useRef(false)

  const fetchNotifications = useCallback(async (showLoading = false) => {
    if (!mounted.current) return
    const tab = currentTab.current
    const requestId = ++requestSequence.current
    const mutationVersion = mutationLock.current.version
    const isCurrentRequest = () => mounted.current
      && requestId === requestSequence.current
      && mutationVersion === mutationLock.current.version
      && tab === currentTab.current

    requestPending.current = true
    if (showLoading) {
      setError(null)
      setLoading(true)
    }

    try {
      const params: Record<string, string> = {}
      if (tab !== 'all') {
        params.type = tab
      }
      const r = await api.get('/v1/notifications', { params })
      if (isCurrentRequest()) {
        setList(r.data.data.list || [])
        setError(null)
      }
    } catch (err) {
      if (isCurrentRequest()) setError(classifyError(err))
    } finally {
      if (requestId === requestSequence.current) requestPending.current = false
      if (isCurrentRequest()) setLoading(false)
    }
  }, [])

  const fetchUnreadCounts = useCallback(async () => {
    const requestId = ++unreadSequence.current
    const mutationVersion = mutationLock.current.version
    try {
      const r = await api.get('/v1/notifications/unread-count')
      if (mounted.current && requestId === unreadSequence.current
        && mutationVersion === mutationLock.current.version) setUnreadCounts(r.data?.data ?? {})
    } catch {
      // Silently ignore unread count fetch failures
    }
  }, [])

  const getTabUnread = useCallback((tabKey: TabKey): number => {
    const data = unreadCounts as { total?: number; by_type?: Record<string, number> }
    const bt = data?.by_type ?? {}
    switch (tabKey) {
      case 'all': return data?.total ?? 0
      case 'interaction': return (bt.like ?? 0) + (bt.comment ?? 0) + (bt.interaction ?? 0) + (bt.at ?? 0)
      case 'follow': return bt.follow ?? 0
      case 'system': return bt.system ?? 0
      default: return 0
    }
  }, [unreadCounts])

  // Load the selected category and invalidate its requests on change or unmount.
  useEffect(() => {
    mounted.current = true
    currentTab.current = activeTab
    fetchNotifications(true)
    fetchUnreadCounts()
    return () => {
      mounted.current = false
      requestSequence.current += 1
      requestPending.current = false
    }
  }, [activeTab, fetchNotifications, fetchUnreadCounts])

  // Task 3.3: Polling with mutation lock guard
  usePolling(
    useCallback(async () => {
      if (mutationLock.current.locked || requestPending.current || pollPending.current) return
      pollPending.current = true
      try {
        await Promise.all([fetchNotifications(), fetchUnreadCounts()])
      } finally {
        pollPending.current = false
      }
    }, [fetchNotifications, fetchUnreadCounts]),
    30000,
  )

  // Task 3.2: Mark notification as read on server
  const markAsRead = useCallback(
    async (notificationId: string) => {
      if (mutationLock.current.locked) return
      mutationLock.current.locked = true
      mutationLock.current.version += 1
      try {
        await api.post('/v1/notifications/read', { ids: [notificationId] })
      } catch {
        toast.error('标记失败，请重试')
      } finally {
        await Promise.all([fetchNotifications(), fetchUnreadCounts()])
        mutationLock.current.locked = false
      }
    },
    [fetchNotifications, fetchUnreadCounts],
  )

  // Task 3.6: Mark all notifications as read
  const handleMarkAllRead = async () => {
    setMarkingAllRead(true)
    setMarkAllFailed(false)
    try {
      await api.post('/v1/notifications/read', { all: true })
      setMarkingAllRead(false)
      // Refresh notifications after marking all read
      await fetchNotifications()
      await fetchUnreadCounts()
    } catch {
      setMarkingAllRead(false)
      setMarkAllFailed(true)
      toast.error('更新失败，已恢复原样')
    }
  }

  // Task 3.6: Retry mark all read
  const handleRetryMarkAll = () => {
    setMarkAllFailed(false)
    handleMarkAllRead()
  }

  // Task 3.2: Keyboard navigation for ARIA tabs
  const handleTabKeyDown = (e: React.KeyboardEvent, currentIndex: number) => {
    let nextIndex = currentIndex
    if (e.key === 'ArrowRight') {
      nextIndex = (currentIndex + 1) % TABS.length
    } else if (e.key === 'ArrowLeft') {
      nextIndex = (currentIndex - 1 + TABS.length) % TABS.length
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      setActiveTab(TABS[currentIndex].key)
      return
    } else {
      return
    }
    e.preventDefault()
    const tabs = document.querySelectorAll<HTMLElement>('[role="tab"]')
    tabs[nextIndex]?.focus()
  }

  return (
    <div className="max-w-4xl mx-auto">
      <PageHeader eyebrow="NOTIFICATIONS" title="通知" description="查看互动、关注与平台消息。" />

      {/* Task 3.6: Mark all read button */}
      <div className="flex items-center gap-2 mb-3">
        <Button variant="outline"
          onClick={handleMarkAllRead}
          disabled={markingAllRead}
        >
          {markingAllRead ? '标记中...' : '全部已读'}
        </Button>
        {markAllFailed && (
          <Button variant="outline" onClick={handleRetryMarkAll}>重试</Button>
        )}
      </div>

      {/* Task 3.1 & 3.2: Category tab bar with ARIA — pill style */}
      <div role="tablist" aria-label="通知分类" className="flex flex-wrap gap-2 border-b border-border pb-5 mb-2">
        {TABS.map((tab, i) => (
          <button
            key={tab.key}
            role="tab"
            aria-selected={activeTab === tab.key}
            tabIndex={activeTab === tab.key ? 0 : -1}
            onClick={() => setActiveTab(tab.key)}
            onKeyDown={(e) => handleTabKeyDown(e, i)}
            className="ef-tab"
          >
            {tab.label}
            {getTabUnread(tab.key) > 0 && (
              <span className="ml-1 text-xs">({getTabUnread(tab.key)})</span>
            )}
          </button>
        ))}
      </div>

      <div>
        {loading ? (
          <ListSkeleton />
        ) : error ? (
          <ErrorState
            error={error}
            onRetry={() => fetchNotifications(true)}
          />
        ) : list.length > 0 ? (
          list.map((n: any) => (
            <div key={n.id} className={cn('ef-record py-5', !n.read && 'border-l-2 border-foreground pl-4')}>
              <div className="flex flex-wrap items-start gap-3 sm:gap-4">
                <span
                  className={cn('inline-flex items-center justify-center size-10 border border-border text-sm font-semibold shrink-0', !n.read ? 'bg-accent text-accent-foreground' : 'bg-muted text-muted-foreground')}
                >
                  {typeLabel[n.type] || '?'}
                </span>
                <div className="flex-1 min-w-0">
                  <span className="text-xs text-muted-foreground">{typeName[n.type] || '通知'}</span>
                  <p className="text-sm break-words leading-relaxed">{n.content || (defaultMessage[n.type] ? `${n.actor_id ? `用户 ${n.actor_id}` : '有人'}${defaultMessage[n.type]}` : '你收到了一条新通知')}</p>
                </div>
                {!n.read && (
                  <button
                    onClick={() => markAsRead(n.id)}
                    className="ef-tab text-xs shrink-0"
                  >
                    标为已读
                  </button>
                )}
              </div>
            </div>
          ))
        ) : (
          <p className="ef-empty">暂时没有新通知。</p>
        )}
      </div>
    </div>
  )
}
