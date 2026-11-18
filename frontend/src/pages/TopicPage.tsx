import React, { useState, useEffect } from 'react'
import PageHeader from '../components/PageHeader'
import { Link, useParams } from 'react-router-dom'
import api from '../api/client'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'
import { buttonVariants } from '../components/ui/button'

export default function TopicPage() {
  const { name, topicId } = useParams<{ name?: string; topicId?: string }>()
  const [detail, setDetail] = useState<any>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)

  useEffect(() => {
    let active = true
    const request = topicId
      ? api.get(`/v1/topics/id/${topicId}`)
      : api.get(`/v1/topics/${encodeURIComponent(name || '')}`)

    setDetail(null)
    setLoading(true)
    setError(null)
    request
      .then((r) => {
        if (active) setDetail(r.data.data)
      })
      .catch((err: unknown) => {
        if (!active) return
        const status = (err as { response?: { status?: number } })?.response?.status
        const classified = classifyError(err)
        setError({
          ...classified,
          message: status === 400 ? '参数无效' : status === 404 ? '话题不存在' : classified.message,
        })
      })
      .finally(() => {
        if (active) setLoading(false)
      })

    return () => {
      active = false
    }
  }, [name, topicId])


  const topicName = detail?.topic?.name || detail?.name || name || topicId || ''

  return (
    <div className="max-w-4xl mx-auto">
      <PageHeader eyebrow="TOPIC" title={`#${topicName}`} description={!loading && !error && detail ? `${detail.total_videos} 个视频` : undefined} />
      {loading && <p role="status" className="text-sm text-muted-foreground py-12 text-center">正在加载话题</p>}
      {!loading && error && (
        <div>
          <ErrorState error={error} />
          <div className="flex justify-center">
            <Link to="/topics" className={buttonVariants({ variant: 'outline', size: 'sm' })}>
              返回话题广场
            </Link>
          </div>
        </div>
      )}
      {!loading && !error && <div>
        {detail?.videos?.map((v: any, index: number) => (
          <Link key={v.id} to={`/videos/${v.id}`} className="ef-record flex items-start gap-4 py-5 hover:bg-muted focus-visible:outline focus-visible:outline-2 focus-visible:outline-foreground">
            <span className="ef-number shrink-0 text-sm text-muted-foreground">{String(index + 1).padStart(2, '0')}</span>
            <h2 className="break-words text-base font-semibold leading-relaxed">{v.title}</h2>
          </Link>
        ))}
      </div>}
      {!loading && !error && detail?.videos?.length === 0 && <p className="ef-empty">这个话题下还没有视频。</p>}
    </div>
  )
}
