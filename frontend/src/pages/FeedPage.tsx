import React, { useState, useEffect, useCallback, useRef } from 'react'
import { useInView } from 'react-intersection-observer'
import { ArrowRight, RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import api from '../api/client'
import { useCollectIntents } from '../lib/collectIntent'
import { useAuth } from '../context/AuthContext'
import { share as shareApi } from '../api/share'
import { likeVideo, unlikeVideo } from '../api/like'
import VideoCard from '../components/VideoCard'
import { FeedSkeleton } from '../components/skeletons/FeedSkeleton'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'
import PageHeader from '../components/PageHeader'
import { Button } from '../components/ui/button'

const TYPES = ['recommend', 'following', 'nearby', 'hot'] as const

const typeLabels: Record<string, string> = { recommend: '推荐', following: '关注', nearby: '同城', hot: '热门' }

// /v1/recommend/for-you 返回裸 Video 列表，归一化为 feed item 形状 {video, stats}
function normalizeRecommendList(raw: any[]): any[] {
  return (raw || []).map((v: any) => (v && v.video ? v : { ...v, video: v }))
}

export default function FeedPage() {
  const [type, setType] = useState<(typeof TYPES)[number]>('recommend')
  const [items, setItems] = useState<any[]>([])
  const [page, setPage] = useState(1)
  const [hasMore, setHasMore] = useState(true)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)
  const [location, setLocation] = useState<{ lng: number; lat: number } | null>(null)
  const [locating, setLocating] = useState(false)
  const [locationError, setLocationError] = useState('')
  const { ref, inView } = useInView()
  const { uid, snapshot } = useAuth()
  const accountScope = `${uid ?? ''}:${snapshot?.generation ?? 0}`
  const collectIntents = useCollectIntents(`${type}:${location?.lng}:${location?.lat}:${accountScope}`)
  const [collectErrors, setCollectErrors] = useState<Record<string, ClassifiedError>>({})
  const tabListRef = useRef<HTMLDivElement>(null)
  const generationRef = useRef(0)
  const requestsRef = useRef(new Set<number>())

  const load = useCallback(async (p: number, generation = generationRef.current) => {
    if (type === 'nearby' && !location) {
      setLoading(false)
      setHasMore(false)
      return
    }
    // Serialize each category generation, including effects from the tab-switch render.
    const key = generation
    if (requestsRef.current.has(key)) return
    requestsRef.current.add(key)
    setError(null)
    setLoading(true)
    try {
      let list: any[]
      let more: boolean
      if (type === 'recommend') {
        try {
          const { data } = await api.get('/v1/recommend/for-you', { params: { page: p, size: 10 } })
          const raw = data?.data?.list || []
          list = normalizeRecommendList(raw)
          more = data?.data?.has_more !== undefined ? data.data.has_more : raw.length >= 10
        } catch {
          // 推荐 API 失败时回退到通用 feed，保证推荐 Tab 不空
          const { data } = await api.get('/v1/feed', { params: { type, page: p, size: 10 } })
          list = data.data.list
          more = data.data.has_more
        }
      } else {
        const { data } = await api.get('/v1/feed', { params: { type, page: p, size: 10, ...(type === 'nearby' ? location : {}) } })
        list = data.data.list
        more = data.data.has_more
      }
      if (generation !== generationRef.current) return
      setItems((prev) => {
        const merged = p === 1 ? [] : [...prev]
        const seen = new Set(merged.map((x: any) => x.video?.id))
        for (const item of list) {
          if (!item.video?.id || seen.has(item.video.id)) continue
          seen.add(item.video.id)
          merged.push(item)
        }
        return merged
      })
      setHasMore(more)
      setPage(p)
    } catch (e) {
      if (generation === generationRef.current) setError(classifyError(e))
    } finally {
      requestsRef.current.delete(key)
      if (generation === generationRef.current) setLoading(false)
    }
  }, [type, location, accountScope])

  useEffect(() => {
    const generation = ++generationRef.current
    setItems([])
    setCollectErrors({})
    setPage(1)
    setHasMore(true)
    setError(null)
    load(1, generation)
    return () => { generationRef.current++ }
  }, [load])
  useEffect(() => {
    if (inView && hasMore && !loading && !error) load(page + 1)
  }, [inView, hasMore, loading, error, page, load])

  // 不可变更新单个 item（配合 VideoCard 的 memo 比较）
  const updateItem = (id: string, patch: (it: any) => any) =>
    setItems((prev) => prev.map((x: any) => (x.video?.id === id ? patch(x) : x)))

  const toggleCollect = async (it: any) => {
    const id = it.video?.id
    if (!id) return
    await collectIntents.run(id, !!it.is_collected, (fact) => {
      updateItem(id, (x) => ({ ...x, is_collected: fact.collected, collect_count: fact.collect_count }))
      setCollectErrors((prev) => { const next = { ...prev }; delete next[id]; return next })
      toast.success(fact.collected ? '已收藏' : '已取消收藏')
    }, (error) => {
      setCollectErrors((prev) => ({ ...prev, [id]: error }))
      toast.error(error.message)
    })
  }
  const toggleLike = async (it: any) => {
    if (it.like_state_known !== true) return
    const id = it.video.id
    const prevState = { liked: it.is_liked, count: it.stats?.like_count || 0 }
    updateItem(id, (x) => ({
      ...x,
      is_liked: !x.is_liked,
      stats: { ...(x.stats || {}), like_count: x.is_liked ? Math.max(0, (x.stats?.like_count || 0) - 1) : (x.stats?.like_count || 0) + 1 },
    }))
    try {
      if (it.is_liked) {
        await unlikeVideo(id)
      } else {
        await likeVideo(id)
      }
      // Re-fetch authoritative count (optional: use response count if available)
      const cnt = await import('../api/like').then((m) => m.getLikeCount(id))
      updateItem(id, (x) => ({ ...x, stats: { ...(x.stats || {}), like_count: cnt.data?.data?.count ?? cnt.data?.count ?? (x.stats?.like_count || 0) } }))
      toast.success(it.is_liked ? '已取消点赞' : '已点赞')
    } catch (error: any) {
      updateItem(id, (x) => ({
        ...x,
        ...(error?.response?.status === 503 ? { like_state_known: false } : {}),
        is_liked: prevState.liked,
        stats: { ...(x.stats || {}), like_count: prevState.count },
      }))
      toast.error('点赞失败，已恢复原样')
    }
  }

  const toggleShare = async (it: any) => {
    const id = it.video.id
    const url = `${window.location.origin}/videos/${id}`
    try {
      const canShare = typeof navigator.share === 'function'
      if (canShare) {
        await navigator.share({ title: it.video.title, url })
      } else {
        await navigator.clipboard.writeText(url)
      }
      toast.success(canShare ? '已分享' : '链接已复制')
      const { data } = await shareApi(id)
      updateItem(id, (x) => ({ ...x, share_count: data.data.share_count }))
    } catch (e: any) {
      if (e?.name !== 'AbortError') {
        // Show fallback — copy URL manually
        try {
          await navigator.clipboard.writeText(url)
          toast.success('链接已复制')
        } catch { toast.error('分享失败，请稍后重试') }
      }
    }
  }

  // Task 3.1: Keyboard navigation for ARIA tabs
  const handleTabKeyDown = (e: React.KeyboardEvent, currentIndex: number) => {
    let nextIndex = currentIndex
    if (e.key === 'ArrowRight') {
      nextIndex = (currentIndex + 1) % TYPES.length
    } else if (e.key === 'ArrowLeft') {
      nextIndex = (currentIndex - 1 + TYPES.length) % TYPES.length
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      handleTabClick(TYPES[currentIndex])
      return
    } else {
      return
    }
    e.preventDefault()
    const tabs = tabListRef.current?.querySelectorAll<HTMLElement>('[role="tab"]')
    tabs?.[nextIndex]?.focus()
  }

  const handleTabClick = (t: (typeof TYPES)[number]) => {
    if (t === type) return
    setType(t)
  }

  const handleRetry = () => {
    setError(null)
    setLoading(true)
    load(items.length ? page + 1 : 1)
  }

  const requestLocation = () => {
    if (!navigator.geolocation) {
      setLocationError('当前浏览器不支持定位，可以切换其他视频分类。')
      return
    }
    setLocating(true)
    setLocationError('')
    navigator.geolocation.getCurrentPosition(
      ({ coords }) => {
        setLocating(false)
        setLocation({ lng: coords.longitude, lat: coords.latitude })
      },
      (err) => {
        setLocating(false)
        setLocationError(err.code === 1
          ? '未获得定位权限，请在浏览器中允许定位后重试。'
          : '暂时无法获取位置，请稍后重试。')
      },
      { enableHighAccuracy: false, timeout: 10000, maximumAge: 300000 },
    )
  }

  return (
    <div>
      <PageHeader eyebrow="DISCOVER" title="发现视频" description="看见新的灵感，也跟上你喜欢的创作者。">
        <div className="ef-number text-sm text-muted-foreground"><span className="ef-number">{items.length}</span> 个视频已加载</div>
      </PageHeader>
      <div className="mb-8 flex flex-wrap items-center justify-between gap-4 border-y border-border py-3">
      <div ref={tabListRef} role="tablist" aria-label="视频分类" className="grid w-full grid-cols-4 gap-1 sm:w-auto sm:flex sm:gap-2">
        {TYPES.map((t, i) => (
          <button
            key={t}
            role="tab"
            id={`feed-tab-${t}`}
            aria-controls="feed-results"
            aria-selected={type === t}
            tabIndex={type === t ? 0 : -1}
            onClick={() => handleTabClick(t)}
            onKeyDown={(e) => handleTabKeyDown(e, i)}
            className="ef-tab min-h-12 px-3 sm:px-6"
          >
            {typeLabels[t] || t}
          </button>
        ))}
      </div>
      <p className="hidden text-xs text-muted-foreground sm:block">{type === 'following' ? '来自你关注的创作者' : type === 'hot' ? '大家正在观看的内容' : type === 'nearby' ? '发现身边的精彩' : '为你发现值得观看的内容'}</p>
      </div>

      <div id="feed-results" role="tabpanel" aria-labelledby={`feed-tab-${type}`} aria-busy={loading}>

      {type === 'nearby' && !location && (
        <div className="ef-empty px-4 py-16 text-center">
          <p className="mb-2 font-semibold">查看附近的视频</p>
          <p className="mb-5 text-sm text-muted-foreground">允许定位后，显示附近 5 公里内的视频。</p>
          {locationError && <p role="alert" className="mb-5 text-sm">{locationError}</p>}
          <Button onClick={requestLocation} disabled={locating}>{locating ? '正在获取位置…' : '使用当前位置'}</Button>
        </div>
      )}

      {/* Error state — highest priority */}
      {error && (
        <ErrorState error={error} onRetry={handleRetry} />
      )}

      {/* Loading skeleton — shown during initial load when no items yet */}
      {!error && loading && items.length === 0 && (
        <FeedSkeleton count={8} />
      )}

      {/* Empty state — following tab */}
      {!error && !loading && items.length === 0 && type === 'following' && (
        <div className="ef-empty flex flex-col items-center justify-center text-center py-16 px-4">
          <p className="text-muted-foreground text-sm mb-4 max-w-xs">
            关注感兴趣的创作者后，他们的新视频会出现在这里
          </p>
          <Button
            onClick={() => handleTabClick('hot')}
          >
            去热门 <ArrowRight className="size-4" aria-hidden="true" />
          </Button>
        </div>
      )}

      {/* Empty state — other tabs */}
      {!error && !loading && items.length === 0 && type !== 'following' && (type !== 'nearby' || location) && (
        <div className="ef-empty px-4 py-16 text-center"><p className="mb-2 font-semibold">这里还没有视频</p><p className="text-sm text-muted-foreground">试试其他分类，发现更多内容。</p></div>
      )}

      {Object.entries(collectErrors).map(([id, error]) => <div role="alert" key={id} className="mb-4 text-sm text-destructive">{error.message}{error.retriable && <Button variant="outline" onClick={() => toggleCollect({ video: { id } })}>重试收藏</Button>}</div>)}
      <div className="grid grid-cols-2 gap-x-3 gap-y-8 sm:gap-x-5 md:grid-cols-3 xl:grid-cols-4 xl:gap-x-6">
        {items.map((it: any) => (
          <VideoCard
            key={it.video.id}
            item={it}
            onCollect={() => toggleCollect(it)}
            onShare={() => toggleShare(it)}
            onLike={() => toggleLike(it)}
          />
        ))}
      </div>
      {hasMore && !error && items.length > 0 && <div ref={ref} className="flex min-h-24 items-center justify-center">{loading && <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><RefreshCw className="size-4" aria-hidden="true" />正在加载更多视频…</p>}</div>}
      {!hasMore && items.length > 0 && <p className="mt-8 border-t border-border py-6 text-center text-xs text-muted-foreground">已看完当前分类的全部视频</p>}
      </div>
    </div>
  )
}

