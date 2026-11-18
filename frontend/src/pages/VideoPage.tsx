import React, { useState, useEffect, useCallback, useRef } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import api from '../api/client'
import { likeVideo, unlikeVideo, getLikeCount } from '../api/like'
import { getComments, postComment, likeComment, unlikeComment, reportComment, deleteComment } from '../api/comment'
import { useCollectIntents } from '../lib/collectIntent'
import { share as shareApi } from '../api/share'
import { updateVideo, deleteVideo } from '../api/videoManage'
import { useAuth } from '../context/AuthContext'
import { Card, CardContent } from '../components/ui/card'
import { Button, buttonVariants } from '../components/ui/button'
import { VideoSkeleton } from '../components/skeletons/VideoSkeleton'
import { ListSkeleton } from '../components/skeletons/ListSkeleton'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'
import DangerConfirm from '../components/DangerConfirm'
import { usePlaybackTelemetry } from '../hooks/usePlaybackTelemetry'
import { toast } from 'sonner'

export default function VideoPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { uid, snapshot } = useAuth()
  const accountScope = `${uid ?? ''}:${snapshot?.generation ?? 0}`
  const [video, setVideo] = useState<any>(null)
  const playbackRef = useRef<HTMLVideoElement>(null)
  const playbackVideo = video?.video ?? video
  usePlaybackTelemetry(playbackRef, playbackVideo?.id, playbackVideo?.content_version, uid, snapshot?.generation)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)
  const videoReadSequence = useRef(0)

  // Interaction state
  const [isLiked, setIsLiked] = useState(false)
  const [likeStateKnown, setLikeStateKnown] = useState(false)
  const [likeCount, setLikeCount] = useState(0)
  const [isCollected, setIsCollected] = useState(false)
  const [collectCount, setCollectCount] = useState(0)
  const [shareCount, setShareCount] = useState(0)
  const [collectError, setCollectError] = useState<ClassifiedError | null>(null)
  const collectIntents = useCollectIntents(`${id ?? ''}:${accountScope}`)

  // Comment state
  const [comments, setComments] = useState<any[]>([])
  const [commentLoading, setCommentLoading] = useState(false)
  const [commentError, setCommentError] = useState<ClassifiedError | null>(null)
  const [similar, setSimilar] = useState<any[]>([])
  const [retryingMedia, setRetryingMedia] = useState(false)
  const [reviewStatus, setReviewStatus] = useState('')
  const [commentPage, setCommentPage] = useState(1)
  const [commentHasMore, setCommentHasMore] = useState(true)
  const [commentText, setCommentText] = useState('')
  const [submittingComment, setSubmittingComment] = useState(false)
  const [replyTarget, setReplyTarget] = useState<string | null>(null)
  const [replyText, setReplyText] = useState('')
  const [submittingReply, setSubmittingReply] = useState(false)
  const [reportModal, setReportModal] = useState<{ commentId: string; open: boolean } | null>(null)
  const [reportReason, setReportReason] = useState('')

  // Edit video state
  const [editingVideo, setEditingVideo] = useState(false)
  const [editTitle, setEditTitle] = useState('')
  const [editDesc, setEditDesc] = useState('')
  const [editTags, setEditTags] = useState('')
  const [savingVideo, setSavingVideo] = useState(false)


  const fetchVideo = useCallback(async () => {
    if (!id) return
    const generation = collectIntents.getGeneration()
    const sequence = ++videoReadSequence.current
    const current = () => sequence === videoReadSequence.current && collectIntents.isCurrent(generation)
    setLoading(true)
    setVideo(null)
    setLikeStateKnown(false)
    setCollectError(null)
    setError(null)
    setReviewStatus('')
    try {
      const r = await api.get(`/v1/videos/${id}`)
      if (current()) setVideo(r.data.data)
    } catch (err) {
      if (!current()) return
      const classified = classifyError(err)
      if (classified.type === 'notfound') {
        const status = (err as any)?.response?.data?.details?.status
        setReviewStatus(typeof status === 'string' ? status : '')
        setVideo(null)
      } else {
        setError(classified)
      }
    } finally {
      if (current()) setLoading(false)
    }
  }, [id, accountScope, snapshot])

  const fetchComments = useCallback(async (page = 1) => {
    if (!id) return
    if (page === 1) setCommentLoading(true)
    setCommentError(null)
    try {
      const r = await getComments(id, page, 20)
      const list = r.data?.data?.list || r.data?.data || []
      const more = r.data?.data?.has_more !== undefined ? r.data.data.has_more : list.length >= 20
      setComments((prev) => {
        if (page === 1) return list
        const seen = new Set(prev.map((c: any) => c.id))
        return [...prev, ...list.filter((c: any) => !seen.has(c.id))]
      })
      setCommentHasMore(more)
      setCommentPage(page)
    } catch (err) {
      setCommentError(classifyError(err))
    } finally {
      setCommentLoading(false)
    }
  }, [id])

  useEffect(() => {
    fetchVideo()
  }, [fetchVideo])

  useEffect(() => {
    if (video) {
      fetchComments(1)
    }
  }, [video, fetchComments])

  // Similar recommendations (backend GET /v1/recommend/similar/:id).
  // Only approved videos are public, so pending/rejected detail views skip it.
  useEffect(() => {
    const current = video?.video || video
    if (!id || current?.status !== 'approved') {
      setSimilar([])
      return
    }
    let cancelled = false
    setSimilar([])
    Promise.resolve(api.get(`/v1/recommend/similar/${id}`, { params: { limit: 6 } }))
      .then((r) => {
        if (cancelled) return
        const payload = r?.data?.data
        const list = Array.isArray(payload) ? payload : (payload?.list ?? payload?.videos ?? [])
        setSimilar(Array.isArray(list) ? list : [])
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [id, video])

  const handleRetryMedia = async () => {
    if (!id || retryingMedia) return
    setRetryingMedia(true)
    try {
      await api.post(`/v1/videos/${id}/media-retry`)
      toast.success('已提交重新转码，请稍后刷新')
    } catch (err: any) {
      const message = err?.response?.data?.message || '重试失败，请稍后再试'
      toast.error(message)
    } finally {
      setRetryingMedia(false)
    }
  }

  // Initialize interaction state from video data
  useEffect(() => {
    if (!video) return
    setLikeCount(video.stats?.like_count || 0)
    setCollectCount(video.collect_count || 0)
    setIsCollected(!!video.is_collected)
    setShareCount(video.share_count || 0)
    setLikeStateKnown(video.like_state_known === true)
    setIsLiked(video.like_state_known === true && video.is_liked === true)
  }, [video, uid, id])


  // Like toggle
  const handleLike = async () => {
    if (!id || !uid || !likeStateKnown) return
    const generation = collectIntents.getGeneration()
    const prev = { liked: isLiked, count: likeCount }
    setIsLiked(!isLiked)
    setLikeCount((c: number) => isLiked ? Math.max(0, c - 1) : c + 1)
    try {
      if (isLiked) {
        await unlikeVideo(id)
      } else {
        await likeVideo(id)
      }
      const cnt = await getLikeCount(id)
      if (!collectIntents.isCurrent(generation)) return
      setLikeCount(cnt.data?.data?.count ?? cnt.data?.count ?? likeCount)
    } catch (error: any) {
      if (!collectIntents.isCurrent(generation)) return
      if (error?.response?.status === 503) setLikeStateKnown(false)
      setIsLiked(prev.liked)
      setLikeCount(prev.count)
    }
  }

  // Persist one explicit intent, retaining it if the write result is unknown.
  const handleCollect = async () => {
    if (!id || !uid) return
    await collectIntents.run(id, isCollected, (fact) => {
      setIsCollected(fact.collected)
      setCollectCount(fact.collect_count)
      setCollectError(null)
    }, setCollectError)
  }
  // Edit video
  const handleStartEdit = () => {
    setEditTitle(v.title || '')
    setEditDesc(v.description || '')
    setEditTags((v.tags || []).join(', '))
    setEditingVideo(true)
  }

  const handleSaveEdit = async () => {
    if (!id) return
    setSavingVideo(true)
    try {
      const tags = editTags.split(',').map((t: string) => t.trim()).filter(Boolean)
      await updateVideo(id, {
        title: editTitle || undefined,
        description: editDesc || undefined,
        tags: tags.length > 0 ? tags : undefined,
      })
      setEditingVideo(false)
      fetchVideo()
    } catch {
      // keep form open
    } finally {
      setSavingVideo(false)
    }
  }

  const handleDeleteVideo = async () => {
    if (!id) return
    try {
      await deleteVideo(id)
      navigate('/', { replace: true })
    } catch {}
  }

  // Share
  const handleShare = async () => {
    if (!id) return
    const url = `${window.location.origin}/videos/${id}`
    try {
      if (navigator.share) {
        await navigator.share({ title: video?.video?.title || video?.title, url })
      } else {
        await navigator.clipboard.writeText(url)
      }
      const { data } = await shareApi(id)
      setShareCount(data.data.share_count)
    } catch (e: any) {
      if (e?.name !== 'AbortError') {
        try { await navigator.clipboard.writeText(url) } catch {}
      }
    }
  }

  // Submit comment
  const handleSubmitComment = async () => {
    if (!id || !uid || !commentText.trim()) return
    setSubmittingComment(true)
    try {
      await postComment(id, commentText.trim())
      setCommentText('')
      fetchComments(1) // Refresh to get the new comment
    } catch {
      // Keep text on error so user doesn't lose input
    } finally {
      setSubmittingComment(false)
    }
  }

  // Submit embedded reply
  const handleSubmitReply = async (parentId: string) => {
    if (!id || !uid || !replyText.trim()) return
    setSubmittingReply(true)
    try {
      await postComment(id, replyText.trim(), parentId)
      setReplyText('')
      setReplyTarget(null)
      fetchComments(1)
    } catch {
      // Keep text on error so user doesn't lose input
    } finally {
      setSubmittingReply(false)
    }
  }

  // Comment like toggle
  const handleCommentLike = async (commentId: string, liked: boolean) => {
    setComments((prev) =>
      prev.map((c: any) =>
        c.id === commentId
          ? { ...c, is_liked: !liked, like_count: (c.like_count || 0) + (liked ? -1 : 1) }
          : c,
      ),
    )
    try {
      if (liked) {
        await unlikeComment(commentId)
      } else {
        await likeComment(commentId)
      }
    } catch (error: any) {
      setComments((prev) =>
        prev.map((c: any) =>
          c.id === commentId
            ? { ...c, is_liked: liked, like_count: (c.like_count || 0) + (liked ? 1 : -1), ...(error?.response?.status === 503 ? { like_state_known: false } : {}) }
            : c,
        ),
      )
    }
  }

  // Report comment
  const handleReportComment = async () => {
    if (!reportModal) return
    try {
      await reportComment(reportModal.commentId, reportReason || '违规内容')
      setReportModal(null)
      setReportReason('')
    } catch {
      // keep modal open on error
    }
  }

  // Delete comment
  const handleDeleteComment = async (commentId: string) => {
    try {
      await deleteComment(commentId)
      setComments((prev) => prev.filter((c: any) => c.id !== commentId))
    } catch {}
  }

  const handleLoadMoreComments = () => {
    if (commentHasMore && !commentLoading) {
      fetchComments(commentPage + 1)
    }
  }

  if (loading) return (
    <div className="max-w-2xl mx-auto mt-8">
      <VideoSkeleton />
    </div>
  )
  if (error) return (
    <div className="max-w-2xl mx-auto mt-8">
      <ErrorState error={error} onRetry={async () => { await fetchVideo() }} />
    </div>
  )
  if (!video) return (
    <div className="text-center mt-16">
      {reviewStatus === 'pending' ? (
        <>
          <p className="text-muted-foreground">视频正在审核中</p>
          <p className="text-sm text-muted-foreground mt-2">审核通过后会出现在这里，你可以稍后刷新查看。</p>
        </>
      ) : reviewStatus === 'rejected' ? (
        <>
          <p className="text-muted-foreground">视频未通过审核</p>
          <p className="text-sm text-muted-foreground mt-2">可以修改内容后重新投稿。</p>
        </>
      ) : (
        <>
          <p className="text-muted-foreground">视频不存在</p>
          <p className="text-sm text-muted-foreground mt-2">链接可能有误，或作品已被删除。</p>
        </>
      )}
      <Link to="/" className={`${buttonVariants({ variant: 'outline' })} mt-4`}>返回首页</Link>
    </div>
  )

  const v = video.video || video
  const isOwner = uid && String(v.author_id) === String(uid)

  return (
    <div className="max-w-7xl mx-auto lg:grid lg:grid-cols-[minmax(0,1fr)_240px] xl:grid-cols-[minmax(0,1fr)_280px] lg:gap-8">
      <div className="min-w-0">
        <div className="aspect-video bg-black overflow-hidden mb-6">
          {v.video_url ? (
            <video ref={playbackRef} src={v.video_url} controls className="w-full h-full" />
          ) : (
            <div className="w-full h-full flex items-center justify-center">
              <svg className="size-16 text-white/30" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>
            </div>
          )}
        </div>

        <p className="ef-eyebrow mb-3">// VIDEO</p>
        <h1 className="text-2xl sm:text-3xl font-bold leading-tight break-words mb-4">{v.title}</h1>
        <div className="flex flex-wrap gap-x-5 gap-y-1 text-sm text-muted-foreground mb-5">
          {v.author_id != null && <span>作者 / {video.author?.nickname || video.user?.nickname || v.author?.nickname || `用户 ${v.author_id}`}</span>}
          <span className="ef-number">{video.stats?.play_count || 0} 次播放</span>
        </div>
        {v.description && <p className="text-sm text-muted-foreground leading-7 break-words mb-5">{v.description}</p>}
        {v.tags?.length > 0 && (
          <div className="flex flex-wrap gap-2 mb-6">
            {v.tags.map((t: string) => (
              <span key={t} className="inline-flex border border-border px-3 py-1 bg-muted text-xs">{t}</span>
            ))}
          </div>
        )}

        {/* Interaction bar */}
        <div className="flex flex-wrap items-center gap-2 mb-6">
          <Button
            variant={isLiked ? 'default' : 'outline'}
            size="sm"
            onClick={handleLike}
            disabled={!uid || !likeStateKnown}
            className="min-h-[48px] gap-1"
            aria-label={!uid ? '点赞' : likeStateKnown ? (isLiked ? '取消点赞' : '点赞') : '点赞暂不可用'}
            aria-pressed={!uid || !likeStateKnown ? undefined : isLiked}
          >
            <span>{isLiked ? '♥' : '♡'}</span>
            <span>{likeStateKnown ? likeCount : "点赞暂不可用"}</span>
          </Button>
          <Button
            variant={isCollected ? 'default' : 'outline'}
            size="sm"
            onClick={handleCollect}
            disabled={!uid}
            className="min-h-[44px] gap-1"
            aria-label={isCollected ? '取消收藏' : '收藏'}
            aria-pressed={uid ? isCollected : undefined}
          >
            <span>{isCollected ? '★' : '☆'}</span>
            <span>{collectCount}</span>
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={handleShare}
            className="min-h-[44px] gap-1"
            aria-label="分享"
          >
            <span>↗</span>
            <span>{shareCount}</span>
          </Button>
          {isOwner && (
            <>
              {String(v.media_status || '') === 'failed' && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleRetryMedia}
                  disabled={retryingMedia}
                  className="min-h-[44px]"
                >
                  {retryingMedia ? '提交中...' : '重新转码'}
                </Button>
              )}
              <Button variant="outline" size="sm" onClick={handleStartEdit} className="min-h-[44px]">
                编辑
              </Button>
              <DangerConfirm
                title="删除视频"
                description={`确认删除视频 **${v.title}**？`}
                confirmText="删除"
                consequenceText="删除后无法恢复"
                requireCountdown
                onConfirm={handleDeleteVideo}
              >
                <Button variant="destructive" size="sm" className="min-h-[44px]">删除</Button>
              </DangerConfirm>
            </>
          )}
        </div>

        {collectError && <div role="alert" className="mb-4 text-sm text-destructive">{collectError.message}{collectError.retriable && <Button variant="outline" onClick={handleCollect}>重试收藏</Button>}</div>}
        {/* Comment section */}
        <div className="border-t pt-6">
          <p className="ef-eyebrow mb-2">// COMMENTS</p>
          <h2 className="text-xl font-bold mb-5">评论</h2>

          {/* Comment input */}
          {uid ? (
            <div className="flex gap-3 mb-6">
              <input
                aria-label="发表评论"
                type="text"
                placeholder="发表评论..."
                value={commentText}
                onChange={(e) => setCommentText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault()
                    handleSubmitComment()
                  }
                }}
                className="border border-input bg-card px-3 py-2 text-base flex-1 min-w-0 min-h-[44px]"
              />
              <Button
                size="sm"
                onClick={handleSubmitComment}
                disabled={!commentText.trim() || submittingComment}
                className="min-h-[44px]"
              >
                {submittingComment ? '发送中...' : '发送'}
              </Button>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground mb-6">
              <Link to="/login" className="text-primary hover:underline">登录</Link>后可以发表评论
            </p>
          )}

          {/* Comment list */}
          {commentLoading && comments.length === 0 ? (
            <ListSkeleton n={3} />
          ) : commentError ? (
            commentError.type === 'notfound' ? (
              <p className="text-center text-muted-foreground py-8 text-sm">评论将在视频审核通过后开放</p>
            ) : (
              <ErrorState error={commentError} onRetry={async () => { await fetchComments(1) }} />
            )
          ) : comments.length === 0 ? (
            <p className="text-center text-muted-foreground py-8 text-sm">暂无评论，来发表第一条评论吧</p>
          ) : (
            <div className="border-t border-border">
              {comments.map((comment: any) => (
                <div key={comment.id} className="ef-record flex gap-3 py-5 border-b border-border last:border-0">
                  <div className="flex-1 min-w-0">
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mb-2">
                      <span className="text-sm font-medium truncate">
                        {comment.user?.nickname || comment.author || `用户 ${comment.user_id || comment.author_id}`}
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {comment.created_at ? new Date(comment.created_at).toLocaleDateString() : ''}
                      </span>
                    </div>
                    <p className="text-sm leading-7 mb-2 break-words">{comment.content}</p>
                    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                      <button
                        type="button"
                        onClick={() => { if (comment.like_state_known === true) void handleCommentLike(comment.id, !!comment.is_liked) }}
                        className="ef-tab text-xs"
                        aria-pressed={comment.like_state_known === true ? !!comment.is_liked : undefined}
                        disabled={!uid || comment.like_state_known !== true}
                      >
                        {comment.like_state_known === true ? `${comment.is_liked ? '♥' : '♡'} ${comment.like_count || 0}` : '点赞暂不可用'}
                      </button>
                      {uid && String(comment.user_id || comment.author_id) !== String(uid) && (
                        <button
                          type="button"
                          onClick={() => setReportModal({ commentId: comment.id, open: true })}
                          className="ef-tab text-xs"
                        >
                          举报
                        </button>
                      )}
                      {uid && (
                        <button
                          type="button"
                          onClick={() => {
                            setReplyTarget(replyTarget === comment.id ? null : comment.id)
                            setReplyText('')
                          }}
                          className="ef-tab text-xs"
                          aria-label="回复评论"
                        >
                          回复
                        </button>
                      )}
                      {uid && String(comment.user_id || comment.author_id) === String(uid) && (
                        <DangerConfirm
                          title="删除评论"
                          description="删除后无法恢复"
                          confirmText="删除"
                          onConfirm={() => handleDeleteComment(comment.id)}
                        >
                          <button type="button" className="ef-tab text-xs text-destructive">删除</button>
                        </DangerConfirm>
                      )}
                    </div>
                    {Array.isArray(comment.replies) && comment.replies.filter((r: any) => r.status !== 'hidden').length > 0 && (
                      <div className="mt-3 space-y-3 border-l border-border pl-4">
                        {comment.replies.filter((r: any) => r.status !== 'hidden').map((reply: any) => (
                          <div key={reply.id}>
                            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                              <span className="font-medium text-foreground">
                                {reply.user?.nickname || reply.author || `用户 ${reply.user_id || reply.author_id}`}
                              </span>
                              <span>{reply.created_at ? new Date(reply.created_at).toLocaleDateString() : ''}</span>
                            </div>
                            <p className="text-sm leading-6 break-words">{reply.content}</p>
                          </div>
                        ))}
                      </div>
                    )}
                    {replyTarget === comment.id && (
                      <div className="mt-3 flex gap-2">
                        <input
                          aria-label="回复内容"
                          placeholder={`回复 ${comment.user?.nickname || comment.author || '这条评论'}...`}
                          value={replyText}
                          onChange={(e) => setReplyText(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter' && !e.shiftKey) {
                              e.preventDefault()
                              handleSubmitReply(comment.id)
                            }
                          }}
                          className="border border-input bg-card px-3 py-2 text-sm flex-1 min-w-0 min-h-[44px]"
                        />
                        <Button
                          size="sm"
                          onClick={() => handleSubmitReply(comment.id)}
                          disabled={!replyText.trim() || submittingReply}
                          className="min-h-[44px]"
                        >
                          {submittingReply ? '发送中...' : '发送'}
                        </Button>
                      </div>
                    )}
                  </div>
                </div>
              ))}
              {commentHasMore && (
                <div className="text-center">
                  <Button variant="outline" size="sm" onClick={handleLoadMoreComments} disabled={commentLoading}>
                    {commentLoading ? '加载中...' : '加载更多'}
                  </Button>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      <aside className="mt-6 lg:mt-0">
      <Card className="hidden lg:block border-0 border-t border-b bg-transparent mb-5">
      <CardContent className="px-0 py-5">
            <div className="ef-number grid grid-cols-2 gap-x-4 gap-y-4 text-sm text-muted-foreground">
              <span>{video.stats?.play_count || 0} 次播放</span>
              <span>{likeCount} 点赞</span>
              <span>{collectCount} 收藏</span>
              <span>{shareCount} 分享</span>
            </div>
          </CardContent>
        </Card>
        {similar.length > 0 && (
          <div className="mb-5">
            <p className="ef-eyebrow mb-2">// SIMILAR</p>
            <h3 className="text-sm font-bold mb-3">相似推荐</h3>
            <ul className="space-y-2">
              {similar.slice(0, 6).map((item: any) => {
                const itemId = item.id || item.video?.id
                const itemTitle = item.title || item.video?.title || '视频'
                if (!itemId) return null
                return (
                  <li key={itemId}>
                    <Link to={`/videos/${itemId}`} className="ef-cut block border border-border bg-card px-3 py-2 text-sm hover:bg-muted">
                      {itemTitle}
                    </Link>
                  </li>
                )
              })}
            </ul>
          </div>
        )}
        <Link to="/" className={buttonVariants({ variant: 'outline' })}>返回首页</Link>
      </aside>

      {/* Edit video modal */}
      {editingVideo && (
        <div className="fixed inset-0 z-[80] flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setEditingVideo(false)} />
          <div className="relative z-10 w-full max-w-md mx-4 max-h-[85dvh] overflow-y-auto bg-card text-card-foreground border border-border p-6">
            <p className="ef-eyebrow mb-2">// EDIT VIDEO</p>
            <h3 className="text-xl font-bold mb-6">编辑视频</h3>
            <div className="space-y-3">
              <div>
                <label htmlFor="video-editTitle" className="text-sm font-medium block mb-2">标题</label>
                <input
                  type="text"
                  id="video-editTitle"
                  value={editTitle}
                  onChange={(e) => setEditTitle(e.target.value)}
                  className="border border-input bg-card px-3 py-2 text-base w-full min-h-[44px]"
                />
              </div>
              <div>
                <label htmlFor="video-editDesc" className="text-sm font-medium block mb-2">描述</label>
                <textarea
                  id="video-editDesc"
                  value={editDesc}
                  onChange={(e) => setEditDesc(e.target.value)}
                  className="border border-input bg-card px-3 py-2 text-base w-full min-h-[100px]"
                />
              </div>
              <div>
                <label htmlFor="video-editTags" className="text-sm font-medium block mb-2">标签（逗号分隔）</label>
                <input
                  type="text"
                  id="video-editTags"
                  value={editTags}
                  onChange={(e) => setEditTags(e.target.value)}
                  className="border border-input bg-card px-3 py-2 text-base w-full min-h-[44px]"
                  placeholder="旅行, 美食, Vlog"
                />
              </div>
            </div>
            <div className="flex justify-end gap-3 mt-6">
              <Button variant="outline" onClick={() => setEditingVideo(false)}>取消</Button>
              <Button onClick={handleSaveEdit} disabled={savingVideo}>
                {savingVideo ? '保存中...' : '保存'}
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Report modal */}
      {reportModal?.open && (
        <div className="fixed inset-0 z-[80] flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setReportModal(null)} />
          <div className="relative z-10 w-full max-w-md mx-4 max-h-[85dvh] overflow-y-auto bg-card text-card-foreground border border-border p-6">
            <p className="ef-eyebrow mb-2">// REPORT COMMENT</p>
            <h3 className="text-xl font-bold mb-4">举报评论</h3>
            <label htmlFor="comment-report-reason" className="block text-sm font-medium mb-2">举报原因</label>
            <textarea
              id="comment-report-reason"
              placeholder="请描述举报原因..."
              value={reportReason}
              onChange={(e) => setReportReason(e.target.value)}
              className="border border-input bg-card px-3 py-2 text-base w-full min-h-[100px] mb-4"
            />
            <div className="flex justify-end gap-3">
              <Button variant="outline" onClick={() => { setReportModal(null); setReportReason('') }}>
                取消
              </Button>
              <Button onClick={handleReportComment}>
                提交举报
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}


