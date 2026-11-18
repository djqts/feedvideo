import React, { useCallback, useEffect, useState } from 'react'
import PageHeader from '../components/PageHeader'
import { getAdminVideos } from '../api/admin'
import { reviewVideo } from '../api/moderation'
import { Card, CardContent } from '../components/ui/card'
import { Button } from '../components/ui/button'
import DangerConfirm from '../components/DangerConfirm'
import ErrorState from '../components/ErrorState'
import { classifyError, type ClassifiedError } from '../lib/classifyError'
import { toast } from 'sonner'

type ReviewStatus = 'pending' | 'rejected' | 'approved'

const statusLabels: Record<ReviewStatus, string> = {
  pending: '待审核',
  rejected: '已驳回',
  approved: '已通过',
}

export default function AdminVideosPage() {
  const [status, setStatus] = useState<ReviewStatus>('pending')
  const [videos, setVideos] = useState<any[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)
  const [busyId, setBusyId] = useState('')

  const loadVideos = useCallback(async (next: ReviewStatus = status) => {
    setLoading(true)
    setError(null)
    try {
      const { data } = await getAdminVideos(next)
      const payload = data?.data
      setVideos(Array.isArray(payload?.list) ? payload.list : [])
      setTotal(Number(payload?.total) || 0)
    } catch (err) {
      setError(classifyError(err))
    } finally {
      setLoading(false)
    }
  }, [status])

  useEffect(() => { void loadVideos(status) }, [status, loadVideos])

  const decide = async (videoId: string, next: 'approved' | 'rejected') => {
    if (!videoId || busyId) return
    setBusyId(videoId)
    try {
      await reviewVideo(videoId, next)
      toast.success(next === 'approved' ? '已通过审核' : '已驳回')
      await loadVideos()
    } catch (err: any) {
      toast.error(err?.response?.data?.message || '操作失败，请稍后重试')
    } finally {
      setBusyId('')
    }
  }

  return (
    <div>
      <PageHeader eyebrow="VIDEOS" title="视频审核" description="审核用户新发布的视频；通过后才会出现在 Feed 与公开详情页。" />

      <div role="tablist" aria-label="审核状态" className="my-6 flex flex-wrap gap-2">
        {(Object.keys(statusLabels) as ReviewStatus[]).map((key) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={status === key}
            className={`ef-tab min-h-11 ${status === key ? 'border-accent bg-accent text-accent-foreground' : ''}`}
            onClick={() => setStatus(key)}
          >
            {statusLabels[key]}
          </button>
        ))}
        <span className="ef-number self-center text-sm text-muted-foreground">共 {total} 条</span>
      </div>

      {loading ? (
        <p role="status" className="ef-empty">正在加载视频…</p>
      ) : error ? (
        <ErrorState error={error} onRetry={() => { void loadVideos() }} />
      ) : (
        <>
          {videos.map((video: any) => {
            const videoId = String(video.id || '')
            return (
              <Card key={videoId} className="ef-record border-0 border-b bg-transparent">
                <CardContent className="w-full px-0 py-5">
                  <div className="flex flex-col xl:flex-row items-start justify-between gap-4">
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium break-words">{video.title || '（无标题）'}</p>
                      {video.description && <p className="text-xs text-muted-foreground mt-1 break-words">{video.description}</p>}
                      <p className="ef-number text-xs text-muted-foreground mt-2">
                        ID {videoId} · 作者 {video.author_id || '-'} · 媒体 {video.media_status || 'unknown'}
                        {video.created_at ? ` · ${new Date(video.created_at).toLocaleString()}` : ''}
                      </p>
                      {Array.isArray(video.tags) && video.tags.length > 0 && (
                        <p className="text-xs text-muted-foreground mt-1">标签：{video.tags.join(' / ')}</p>
                      )}
                    </div>
                    <div className="flex flex-wrap gap-2 shrink-0">
                      {status === 'pending' && (
                        <>
                          <Button size="sm" disabled={busyId === videoId} onClick={() => decide(videoId, 'approved')}>
                            {busyId === videoId ? '处理中…' : '通过'}
                          </Button>
                          <DangerConfirm
                            title="驳回视频"
                            description={`确认驳回视频 **${video.title || videoId}**？`}
                            confirmText="驳回"
                            consequenceText="驳回后作者需要重新投稿，公开页面不可见"
                            onConfirm={() => decide(videoId, 'rejected')}
                          >
                            <Button variant="outline" size="sm">驳回</Button>
                          </DangerConfirm>
                        </>
                      )}
                      {status === 'rejected' && (
                        <Button size="sm" disabled={busyId === videoId} onClick={() => decide(videoId, 'approved')}>
                          {busyId === videoId ? '处理中…' : '恢复并通过'}
                        </Button>
                      )}
                      {status === 'approved' && (
                        <DangerConfirm
                          title="下架视频"
                          description={`确认下架视频 **${video.title || videoId}**？`}
                          confirmText="下架"
                          consequenceText="下架后公开页面不可见"
                          onConfirm={() => decide(videoId, 'rejected')}
                        >
                          <Button variant="outline" size="sm">下架</Button>
                        </DangerConfirm>
                      )}
                    </div>
                  </div>
                </CardContent>
              </Card>
            )
          })}
          {videos.length === 0 && <p className="ef-empty">{status === 'pending' ? '没有待审核的视频。' : '这个状态下没有视频。'}</p>}
        </>
      )}
    </div>
  )
}
