import React, { useState, useEffect, useCallback } from 'react'
import PageHeader from '../components/PageHeader'
import { Link, useNavigate } from 'react-router-dom'
import api from '../api/client'
import { createTopic } from '../api/topic'
import { useAuth } from '../context/AuthContext'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import { GridSkeleton } from '../components/skeletons/GridSkeleton'
import ErrorState from '../components/ErrorState'

export default function TopicPlaza() {
  const { uid } = useAuth()
  const navigate = useNavigate()
  const [topics, setTopics] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)

  // Create topic modal
  const [showCreate, setShowCreate] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDesc, setNewDesc] = useState('')
  const [creating, setCreating] = useState(false)

  const fetchTopics = useCallback(() => {
    setLoading(true)
    setError(null)
    api.get('/v1/topics/hot')
      .then((r) => {
        setTopics(r.data.data || [])
        setLoading(false)
      })
      .catch((err: unknown) => {
        setError(classifyError(err))
        setLoading(false)
      })
  }, [])

  useEffect(() => { fetchTopics() }, [fetchTopics])


  // Topic ID may be a zero ObjectID ("000...0") when the topic only exists
  // in the hot ranking but not in the topics collection — treat as invalid.
  const topicLink = (t: any) => {
    const id = t.id
    const isValidId = id && id !== '000000000000000000000000'
    return isValidId ? `/topics/id/${id}` : `/topics/${encodeURIComponent(t.name)}`
  }

  const handleCreate = async () => {
    if (!newName.trim()) return
    setCreating(true)
    try {
      const { data } = await createTopic(newName.trim(), newDesc.trim() || undefined)
      setShowCreate(false)
      setNewName('')
      setNewDesc('')
      const topicName = data.data?.name || data.name || newName.trim()
      navigate(`/topics/${encodeURIComponent(topicName)}`)
    } catch {
      // keep modal open
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="max-w-6xl mx-auto">
      <PageHeader eyebrow="TOPICS" title="话题广场" description="找到感兴趣的话题，发现一起分享的人。">
        {uid && (
          <Button variant="outline" size="sm" onClick={() => setShowCreate(true)}>
            + 创建话题
          </Button>
        )}
      </PageHeader>

      {/* Loading skeleton */}
      {loading && topics.length === 0 && <GridSkeleton rows={2} cols={3} />}

      {/* Error state with retry */}
      {!loading && error && (
        <ErrorState error={error} onRetry={fetchTopics} />
      )}

      {/* Topic grid */}
      {!loading && !error && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-8">
          {topics.map((t: any, index: number) => (
            <Link key={t.name} to={topicLink(t)} className="ef-record group flex min-h-24 items-center gap-4 py-5">
              <span className="ef-number text-sm text-muted-foreground">{String(index + 1).padStart(2, '0')}</span>
              <div className="min-w-0 flex-1">
                <h2 className="break-words text-lg font-semibold group-hover:underline">#{t.name}</h2>
                {t.total_videos != null && <div className="ef-number mt-1 text-xs text-muted-foreground">{t.total_videos} 个视频</div>}
              </div>
              <span aria-hidden="true" className="text-lg">↗</span>
            </Link>
          ))}
        </div>
      )}

      {/* Empty state */}
      {!loading && !error && topics.length === 0 && (
        <p className="ef-empty">这里将展示大家正在分享的话题。</p>
      )}

      {/* Create topic modal */}
      {showCreate && (
        <div className="fixed inset-0 z-[80] flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setShowCreate(false)} />
          <div className="relative z-10 w-full max-w-md mx-4 max-h-[85dvh] overflow-y-auto bg-card text-card-foreground border border-border p-6" role="dialog" aria-modal="true" aria-labelledby="create-topic-title">
            <p className="ef-eyebrow mb-2">// CREATE TOPIC</p>
            <h3 className="text-xl font-bold mb-6" id="create-topic-title">创建话题</h3>
            <div className="space-y-3">
              <div>
                <label htmlFor="topic-name" className="text-sm font-medium block mb-2">话题名称 *</label>
                <Input
                  id="topic-name"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="输入话题名称，如 #旅行日记"
                  className="min-h-[48px]"
                />
              </div>
              <div>
                <label htmlFor="topic-description" className="text-sm font-medium block mb-2">话题描述</label>
                <textarea
                  id="topic-description"
                  value={newDesc}
                  onChange={(e) => setNewDesc(e.target.value)}
                  className="border border-input bg-card px-3 py-2 text-base w-full min-h-[100px]"
                  placeholder="简要描述话题内容（可选）"
                />
              </div>
            </div>
            <div className="flex justify-end gap-3 mt-6">
              <Button variant="outline" onClick={() => setShowCreate(false)}>取消</Button>
              <Button onClick={handleCreate} disabled={!newName.trim() || creating}>
                {creating ? '创建中...' : '创建'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
