import React, { useState, useEffect, useCallback, useRef } from 'react'
import PageHeader from '../components/PageHeader'
import { useInView } from 'react-intersection-observer'
import { getCollectedVideos } from '../api/collect'
import { useCollectIntents } from '../lib/collectIntent'
import { useAuth } from '../context/AuthContext'
import { Button } from '../components/ui/button'
import { share as shareApi } from '../api/share'
import { likeVideo, unlikeVideo } from '../api/like'
import VideoCard from '../components/VideoCard'
import { FeedSkeleton } from '../components/skeletons/FeedSkeleton'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'

export default function CollectionsPage() {
  const [items, setItems] = useState<any[]>([])
  const [page, setPage] = useState(0)
  const [hasMore, setHasMore] = useState(true)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)
  const { ref, inView } = useInView()
  const { uid, snapshot } = useAuth()
  const scope = `${uid ?? ''}:${snapshot?.generation ?? 0}`
  const collectIntents = useCollectIntents(scope)
  const [collectErrors, setCollectErrors] = useState<Record<string, ClassifiedError>>({})
  const requestsRef = useRef(new Set<number>())
  const failedPageRef = useRef(1)

  const load = useCallback(async (p: number) => {
    const generation = collectIntents.getGeneration()
    if (requestsRef.current.has(generation)) return
    requestsRef.current.add(generation)
    setLoading(true)
    setError(null)
    try {
      const { data } = await getCollectedVideos(p, 10)
      if (!collectIntents.isCurrent(generation)) return
      const payload = data.data
      const videos = Array.isArray(payload) ? payload : Array.isArray(payload?.list) ? payload.list : []
      // The collection endpoint returns bare Video records; cards use feed items.
      const list = videos.map((item: any) => ({
        ...item,
        video: item.video || item,
        is_collected: true,
      }))
      setItems((prev) => {
        if (p === 1) return list
        const seen = new Set(prev.map((x: any) => x.video?.id))
        return [...prev, ...list.filter((x: any) => !seen.has(x.video?.id))]
      })
      setHasMore(typeof payload?.has_more === 'boolean' ? payload.has_more : list.length >= 10)
      setPage(p)
    } catch (e) {
      if (collectIntents.isCurrent(generation)) {
        failedPageRef.current = p
        setError(classifyError(e))
      }
    } finally {
      requestsRef.current.delete(generation)
      if (collectIntents.isCurrent(generation)) setLoading(false)
    }
  }, [scope])

  useEffect(() => {
    setItems([])
    setPage(0)
    setHasMore(true)
    setCollectErrors({})
    load(1)
  }, [load])
  useEffect(() => {
    if (inView && hasMore && !loading && !error) load(page + 1)
  }, [inView, hasMore, loading, error, page, load])

  const updateItem = (id: string, patch: (it: any) => any) =>
    setItems((prev) => prev.map((x: any) => (x.video?.id === id ? patch(x) : x)))

  const toggleCollect = async (it: any) => {
    const id = it.video?.id
    if (!id) return
    await collectIntents.run(id, true, (fact) => {
      updateItem(id, (x) => ({ ...x, is_collected: fact.collected, collect_count: fact.collect_count }))
      if (!fact.collected) setItems((prev) => prev.filter((x: any) => x.video?.id !== id))
      setCollectErrors((prev) => { const next = { ...prev }; delete next[id]; return next })
    }, (error) => setCollectErrors((prev) => ({ ...prev, [id]: error })), false)
  }
  const toggleLike = async (it: any) => {
    const id = it.video?.id
    if (!id || it.like_state_known !== true) return
    const prev = { liked: it.is_liked, count: it.stats?.like_count || 0 }
    updateItem(id, (x) => ({
      ...x,
      is_liked: !x.is_liked,
      stats: { ...(x.stats || {}), like_count: x.is_liked ? Math.max(0, (x.stats?.like_count || 0) - 1) : (x.stats?.like_count || 0) + 1 },
    }))
    try {
      if (it.is_liked) await unlikeVideo(id)
      else await likeVideo(id)
    } catch (error: any) {
      updateItem(id, (x) => ({ ...x, ...(error?.response?.status === 503 ? { like_state_known: false } : {}), is_liked: prev.liked, stats: { ...(x.stats || {}), like_count: prev.count } }))
    }
  }

  const toggleShare = async (it: any) => {
    const id = it.video?.id
    if (!id) return
    const url = `${window.location.origin}/videos/${id}`
    try {
      if (navigator.share) await navigator.share({ title: it.video?.title, url })
      else await navigator.clipboard.writeText(url)
      const { data } = await shareApi(id)
      updateItem(id, (x) => ({ ...x, share_count: data.data.share_count }))
    } catch (e: any) {
      if (e?.name !== 'AbortError') {
        try { await navigator.clipboard.writeText(url) } catch {}
      }
    }
  }

  if (error) return <ErrorState error={error} onRetry={async () => { await load(failedPageRef.current) }} />
  if (loading && items.length === 0) return <FeedSkeleton count={8} />

  return (
    <div>
      <PageHeader eyebrow="COLLECTIONS" title="我的收藏" description="收藏喜欢的视频，随时回来继续观看。" />
      {Object.entries(collectErrors).map(([id, error]) => <div role="alert" key={id} className="mb-4 text-sm text-destructive">{error.message}{error.retriable && <Button variant="outline" onClick={() => toggleCollect({ video: { id } })}>重试收藏</Button>}</div>)}
      {items.length === 0 ? (
        <p className="ef-empty">还没有收藏任何视频，喜欢的内容可以收藏到这里。</p>
      ) : (
        <div className="grid grid-cols-1 min-[390px]:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-x-5 gap-y-8">
          {items.map((it: any) => (
            <VideoCard
              key={it.video?.id}
              item={it}
              onCollect={() => toggleCollect(it)}
              onShare={() => toggleShare(it)}
              onLike={() => toggleLike(it)}
            />
          ))}
        </div>
      )}
      {hasMore && <div ref={ref} className="h-10" />}
    </div>
  )
}

