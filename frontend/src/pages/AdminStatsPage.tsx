import React, { useState, useEffect } from 'react'
import PageHeader from '../components/PageHeader'
import { toast } from 'sonner'
import api from '../api/client'
import { Card, CardContent } from '../components/ui/card'
import { Button } from '../components/ui/button'
import { getBehaviorStats, replayDlq, postSystemNotification, replayOutbox, replayMediaDlq, deleteNotifications, syncVideoStats } from '../api/admin'

const platformMetrics = [
  { key: 'total_videos', label: '总视频' },
  { key: 'total_users', label: '总用户' },
  { key: 'today_plays', label: '今日播放' },
  { key: 'today_uploads', label: '今日上传' },
]

const actionLabels: Record<string, string> = { play: '播放', like: '点赞', collect: '收藏', share: '分享', follow: '关注' }

export default function AdminStatsPage() {
  const [stats, setStats] = useState<any>(null)
  const [behavior, setBehavior] = useState<any>(null)
  const [dlqResult, setDlqResult] = useState<string>('')
  const [dlqCount, setDlqCount] = useState(100)
  const [noticeContent, setNoticeContent] = useState('')
  const [noticeAll, setNoticeAll] = useState(true)
  const [noticeUids, setNoticeUids] = useState('')
  const [sending, setSending] = useState(false)
  const [loading, setLoading] = useState(true)
  const [taskResult, setTaskResult] = useState('')
  const [taskBusy, setTaskBusy] = useState('')
  const [syncIds, setSyncIds] = useState('')
  const [noticeIds, setNoticeIds] = useState('')

  useEffect(() => {
    api.get('/v1/admin/stats').then((r) => {
      setStats(r.data.data)
    }).catch(() => {}).finally(() => setLoading(false))
    // 平台范围行为统计（近 7 天）
    getBehaviorStats(7, 'platform').then((r) => {
      setBehavior(r.data?.data?.actions ?? null)
    }).catch(() => {})
  }, [])

  const replay = async () => {
    setDlqResult('')
    try {
      const r = await replayDlq(dlqCount)
      setDlqResult(`已重放 ${r.data?.data?.requeued ?? 0} 条死信消息`)
      toast.success('DLQ 重放完成')
    } catch {
      setDlqResult('重放失败（MQ 不可用或权限不足）')
      toast.error('DLQ 重放失败')
    }
  }

  const runTask = async (name: string, fn: () => Promise<string>) => {
    if (taskBusy) return
    setTaskBusy(name)
    setTaskResult('')
    try {
      setTaskResult(await fn())
      toast.success(`${name}完成`)
    } catch {
      setTaskResult(`${name}失败（MQ/存储不可用或权限不足）`)
      toast.error(`${name}失败`)
    } finally {
      setTaskBusy('')
    }
  }

  const replayOutboxEvents = () => runTask('重放 outbox', async () => {
    const r = await replayOutbox(100)
    return `已重放 ${r.data?.data?.replayed ?? 0} 条 outbox 事件`
  })

  const replayMediaDeadLetters = () => runTask('重放媒体 DLQ', async () => {
    const r = await replayMediaDlq(50)
    return `已重放 ${r.data?.data?.replayed ?? 0} 条媒体死信`
  })

  const syncStats = () => runTask('同步视频统计', async () => {
    const ids = syncIds.split(',').map((s) => s.trim()).filter(Boolean)
    if (ids.length === 0) throw new Error('empty ids')
    const r = await syncVideoStats(ids.slice(0, 100))
    return `成功 ${r.data?.data?.success ?? 0} 条，失败 ${r.data?.data?.failed ?? 0} 条`
  })

  const removeNotifications = () => runTask('删除通知', async () => {
    const ids = noticeIds.split(',').map((s) => s.trim()).filter(Boolean)
    if (ids.length === 0) throw new Error('empty ids')
    const r = await deleteNotifications(ids)
    return `已删除 ${r.data?.data?.deleted ?? 0} 条通知`
  })

  const sendNotice = async () => {
    if (!noticeContent.trim()) {
      toast.error('请输入通知内容')
      return
    }
    if (!noticeAll && !noticeUids.trim()) {
      toast.error('请填写接收 UID（逗号分隔）')
      return
    }
    setSending(true)
    try {
      await postSystemNotification({
        content: noticeContent.trim(),
        all: noticeAll,
        recipient_ids: noticeAll
          ? undefined
          : noticeUids.split(',').map((s) => s.trim()).filter(Boolean),
      })
      toast.success(noticeAll ? '已广播给全部用户' : '通知已发送')
      setNoticeContent('')
    } catch {
      toast.error('发送失败（请检查权限/参数）')
    } finally {
      setSending(false)
    }
  }


  if (loading) return <div><PageHeader eyebrow="PLATFORM DATA" title="平台数据" description="查看平台内容与用户数据，处理后台任务。" /><div role="status" aria-label="正在加载平台数据" className="grid grid-cols-2 gap-3">{Array.from({length:4}).map((_,i)=><Card key={i}><CardContent className="p-6 animate-pulse"><div className="h-12 bg-muted"/></CardContent></Card>)}</div></div>

  return (
    <div>
      <PageHeader eyebrow="PLATFORM DATA" title="平台数据" description="查看平台内容与用户数据，处理后台任务。" />
      {stats && (
        <div className="grid grid-cols-2 xl:grid-cols-4 border-t border-l border-border">
          {platformMetrics.map((m) => (
            <Card key={m.key} className="ef-metric border-0 border-r border-b">
              <CardContent className="p-5 sm:p-6">
                <div className="ef-number break-words text-2xl sm:text-4xl font-bold mb-2">{(stats[m.key] ?? 0).toLocaleString()}</div>
                <div className="text-xs text-muted-foreground">{m.label}</div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      {!stats && !loading && <p className="ef-empty">平台数据暂时不可用。</p>}

      <section className="mt-10 space-y-8">
        <Card className="border-0 border-t bg-transparent">
          <CardContent className="px-0 py-6">
            <h2 className="text-lg font-bold mb-4">行为统计（近 7 天 / 全平台）</h2>
            {behavior ? (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-8">
                {['play', 'like', 'collect', 'share', 'follow'].map((k) => (
                  <div key={k} className="flex justify-between border-b border-border py-3 text-sm">
                    <span className="text-muted-foreground">{actionLabels[k]}</span>
                    <span className="ef-number font-semibold">{(behavior[k] ?? 0).toLocaleString()}</span>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">暂无行为数据</p>
            )}
          </CardContent>
        </Card>

        <Card className="border-0 border-t bg-transparent">
          <CardContent className="px-0 py-6 space-y-4">
            <h2 className="text-lg font-bold">死信队列重放</h2>
            <label htmlFor="dlq-count" className="block text-sm font-medium">重放条数</label>
            <div className="flex flex-wrap items-center gap-3">
              <input
                id="dlq-count"
                type="number"
                min={1}
                max={1000}
                value={dlqCount}
                onChange={(e) => setDlqCount(Number(e.target.value))}
                aria-label="重放条数"
                className="border border-input bg-card px-3 py-2 text-base min-h-12 w-28"
              />
              <Button size="sm" onClick={replay}>重放 DLQ</Button>
            </div>
            {dlqResult && <p className="text-xs text-muted-foreground">{dlqResult}</p>}
          </CardContent>
        </Card>

        <Card className="border-0 border-t bg-transparent">
          <CardContent className="px-0 py-6 space-y-4">
            <h2 className="text-lg font-bold">后台任务</h2>
            <div className="flex flex-wrap gap-3">
              <Button size="sm" variant="outline" disabled={taskBusy !== ''} onClick={replayOutboxEvents}>
                {taskBusy === '重放 outbox' ? '处理中…' : '重放 outbox 失败事件'}
              </Button>
              <Button size="sm" variant="outline" disabled={taskBusy !== ''} onClick={replayMediaDeadLetters}>
                {taskBusy === '重放媒体 DLQ' ? '处理中…' : '重放媒体死信'}
              </Button>
            </div>
            <div className="flex flex-wrap items-end gap-3">
              <label htmlFor="sync-ids" className="text-sm font-medium">同步视频统计（逗号分隔 ID）</label>
              <input
                id="sync-ids"
                value={syncIds}
                onChange={(e) => setSyncIds(e.target.value)}
                placeholder="如 6afc4c2b20d2725ee01a50f7,6af83783..."
                className="border border-input bg-card px-3 py-2 text-sm min-h-11 flex-1 min-w-[240px]"
              />
              <Button size="sm" variant="outline" disabled={taskBusy !== '' || !syncIds.trim()} onClick={syncStats}>同步统计</Button>
            </div>
            <div className="flex flex-wrap items-end gap-3">
              <label htmlFor="notice-ids" className="text-sm font-medium">删除通知（逗号分隔 ID）</label>
              <input
                id="notice-ids"
                value={noticeIds}
                onChange={(e) => setNoticeIds(e.target.value)}
                placeholder="通知 ID"
                className="border border-input bg-card px-3 py-2 text-sm min-h-11 flex-1 min-w-[240px]"
              />
              <Button size="sm" variant="outline" disabled={taskBusy !== '' || !noticeIds.trim()} onClick={removeNotifications}>删除通知</Button>
            </div>
            {taskResult && <p className="text-xs text-muted-foreground">{taskResult}</p>}
          </CardContent>
        </Card>

        <Card className="border-0 border-t bg-transparent">
          <CardContent className="px-0 py-6 space-y-4">
            <h2 className="text-lg font-bold">系统通知广播</h2>
            <label htmlFor="admin-notice" className="block text-sm font-medium">通知内容</label>
            <textarea
              id="admin-notice"
              rows={3}
              value={noticeContent}
              onChange={(e) => setNoticeContent(e.target.value)}
              placeholder="通知内容"
              aria-label="通知内容"
              className="border border-input bg-card px-3 py-2 text-base w-full min-h-28"
            />
            <div className="flex flex-wrap items-center gap-4 text-sm">
              <label className="flex min-h-11 items-center gap-2">
                <input type="radio" checked={noticeAll} onChange={() => setNoticeAll(true)} />
                全部用户
              </label>
              <label className="flex min-h-11 items-center gap-2">
                <input type="radio" checked={!noticeAll} onChange={() => setNoticeAll(false)} />
                指定 UID
              </label>
            </div>
            {!noticeAll && (
              <input
                value={noticeUids}
                onChange={(e) => setNoticeUids(e.target.value)}
                placeholder="如 26,27（逗号分隔）"
                aria-label="接收 UID"
                className="border border-input bg-card px-3 py-2 text-base min-h-11 w-full"
              />
            )}
            <Button size="sm" onClick={sendNotice} disabled={sending}>
              {sending ? '发送中…' : '发送通知'}
            </Button>
          </CardContent>
        </Card>
      </section>
    </div>
  )
}
