import React, { useState, useEffect } from 'react'
import PageHeader from '../components/PageHeader'
import { getReports, banUser, muteUser, restoreComment } from '../api/moderation'
import { Card, CardContent } from '../components/ui/card'
import { Button } from '../components/ui/button'
import DangerConfirm from '../components/DangerConfirm'
import ErrorState from '../components/ErrorState'
import { classifyError, type ClassifiedError } from '../lib/classifyError'
import { toast } from 'sonner'

export default function AdminReportsPage() {
  const [reports, setReports] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)

  const loadReports = async () => {
    setLoading(true)
    setError(null)
    try {
      const { data } = await getReports()
      const payload = data.data
      setReports(Array.isArray(payload) ? payload : Array.isArray(payload?.list) ? payload.list : [])
    } catch (err) {
      setError(classifyError(err))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadReports() }, [])


  const handleBan = async (reportedUserId: string) => {
    try {
      await banUser(reportedUserId, true)
      loadReports()
    } catch {}
  }

  const handleMute = async (reportedUserId: string) => {
    try {
      await muteUser(reportedUserId, true)
      loadReports()
    } catch {}
  }

  const handleRestore = async (commentId: string) => {
    if (!commentId) return
    try {
      const { data } = await restoreComment(commentId)
      toast.success(data?.data?.restored ? '评论已恢复' : '评论无需恢复')
      loadReports()
    } catch {
      toast.error('恢复失败，请稍后再试')
    }
  }

  return (
    <div>
      <PageHeader eyebrow="REPORTS" title="举报列表" description="查看举报内容与用户信息，谨慎处理每条举报。" />
      {loading ? <p role="status" className="ef-empty">正在加载举报…</p> : error ? <ErrorState error={error} onRetry={loadReports} /> : <>
      <div>
        {reports.map((r: any) => (
          <Card key={r.id} className="ef-record border-0 border-b bg-transparent">
            <CardContent className="w-full px-0 py-5">
              <div className="flex flex-col xl:flex-row items-start justify-between gap-4">
                <div className="flex-1 min-w-0">
                  <span className="inline-block text-xs px-2 py-1 bg-accent text-accent-foreground mb-2">
                    {r.reason || '违规内容'}
                  </span>
                  <p className="text-sm break-words">{r.content || r.comment_id || `举报 ID: ${r.id}`}</p>
                  {r.reported_user_id && (
                    <p className="text-xs text-muted-foreground mt-1">被举报用户: {r.reported_user_id}</p>
                  )}
                  <p className="mt-2 text-xs">
                    <span className={`inline-block px-2 py-1 ${r.status === 'hidden' ? 'bg-destructive/10 text-destructive' : 'bg-muted text-muted-foreground'}`}>
                      评论状态：{r.status === 'hidden' ? '已隐藏' : r.status === 'active' ? '可见' : r.status || '未知'}
                    </span>
                  </p>
                </div>
                <div className="flex flex-wrap gap-2 shrink-0">
                  {r.comment_id && r.status === 'hidden' && (
                    <Button variant="outline" size="sm" onClick={() => handleRestore(r.comment_id)}>恢复评论</Button>
                  )}
                  {r.reported_user_id && (
                    <>
                      <DangerConfirm
                        title="封禁用户"
                        description={`确认封禁用户 **${r.reported_user_id}**？`}
                        confirmText="封禁"
                        consequenceText="封禁后该用户将无法登录和使用平台"
                        requireCountdown
                        onConfirm={() => handleBan(r.reported_user_id)}
                      >
                        <Button variant="outline" size="sm">封禁用户</Button>
                      </DangerConfirm>
                      <DangerConfirm
                        title="禁言用户"
                        description={`确认禁言用户 **${r.reported_user_id}**？`}
                        confirmText="禁言"
                        consequenceText="禁言后该用户将无法发布评论和视频"
                        onConfirm={() => handleMute(r.reported_user_id)}
                      >
                        <Button variant="outline" size="sm">禁言</Button>
                      </DangerConfirm>
                    </>
                  )}
                </div>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      {reports.length === 0 && <p className="ef-empty">目前没有需要处理的举报。</p>}
      </>}
    </div>
  )
}
