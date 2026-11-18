import React, { useState, useEffect, useCallback, useRef } from 'react'
import PageHeader from '../components/PageHeader'
import { Link } from 'react-router-dom'
import api from '../api/client'
import { Card, CardContent } from '../components/ui/card'
import { buttonVariants } from '../components/ui/button'
import { StatsSkeleton } from '../components/skeletons/StatsSkeleton'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'

const metrics = [
  { key: 'total_plays', label: '播放' },
  { key: 'total_likes', label: '点赞' },
  { key: 'total_comments', label: '评论' },
  { key: 'total_shares', label: '分享' },
  { key: 'total_collects', label: '收藏' },
  { key: 'follower_count', label: '粉丝' },
] as const

const periods = [
  { value: 7, label: '7天' },
  { value: 30, label: '30天' },
  { value: 90, label: '90天' },
]

interface TrendPoint {
  date: string
  play_count: number
  collect_count: number
  share_count: number
}

interface CreatorSummary {
  stats: Record<(typeof metrics)[number]['key'] | 'video_count', number> & {
    scope?: string
    play_basis?: string
    trend?: TrendPoint[]
  }
  revenue: { estimated_revenue: number; total_plays: number; cpm: number; currency?: string | null }
}

function isCreatorSummary(data: unknown): data is CreatorSummary {
  if (data === null || typeof data !== 'object' || Array.isArray(data)) return false
  const { stats, revenue } = data as CreatorSummary
  if (!stats || !revenue || typeof stats !== 'object' || typeof revenue !== 'object' || Array.isArray(stats) || Array.isArray(revenue)) return false
  if (![...metrics.map((m) => m.key), 'video_count'].every((key) => {
    const value = stats[key as keyof typeof stats]
    return typeof value === 'number' && Number.isFinite(value) && Number.isInteger(value) && value >= 0
  })) return false
  return revenue.total_plays === stats.total_plays
    && typeof revenue.estimated_revenue === 'number' && Number.isFinite(revenue.estimated_revenue) && revenue.estimated_revenue >= 0
    && typeof revenue.cpm === 'number' && Number.isFinite(revenue.cpm) && revenue.cpm > 0
}

/** Known currency code → symbol mapping. */
const CURRENCY_SYMBOLS: Record<string, string> = {
  CNY: '¥',
  JPY: '¥',
  USD: '$',
  EUR: '€',
  GBP: '£',
  KRW: '₩',
}

/** Returns the display symbol for known currencies. */
function formatCurrency(currency: string | null | undefined): string | null {
  if (currency == null || !Object.prototype.hasOwnProperty.call(CURRENCY_SYMBOLS, currency)) return null
  return CURRENCY_SYMBOLS[currency]
}

const trendLines = [
  { key: 'play_count' as const, dash: undefined, label: '播放' },
  { key: 'collect_count' as const, dash: '8 4', label: '收藏' },
  { key: 'share_count' as const, dash: '2 4', label: '分享' },
]

/** Pure SVG trend chart — no library dependency. */
function TrendChart({ data }: { data: TrendPoint[] }) {
  if (!data || data.length === 0) return null

  const W = 400
  const H = 220
  const PAD = { top: 20, right: 10, bottom: 32, left: 44 }
  const innerW = W - PAD.left - PAD.right
  const innerH = H - PAD.top - PAD.bottom

  // Compute global max for Y scale
  const maxVal = Math.max(
    ...data.flatMap((d) => trendLines.map((t) => d[t.key] ?? 0)),
    1,
  )

  const n = data.length
  const getX = (i: number) => PAD.left + (i / Math.max(n - 1, 1)) * innerW
  const getY = (v: number) => PAD.top + innerH - (v / maxVal) * innerH

  return (
    <div className="mt-4">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="w-full h-[200px]"
        role="img"
        aria-label="趋势图表"
      >
        <g className="text-muted-foreground" fontSize="10">
          <text x="0" y={PAD.top + 4} fill="currentColor">{maxVal.toLocaleString()}</text>
          <text x="0" y={PAD.top + innerH + 4} fill="currentColor">0</text>
          <text x={PAD.left} y={H - 8} fill="currentColor">{data[0].date}</text>
          <text x={W - PAD.right} y={H - 8} textAnchor="end" fill="currentColor">{data[n - 1].date}</text>
        </g>
        <line x1={PAD.left} y1={PAD.top + innerH} x2={W - PAD.right} y2={PAD.top + innerH} stroke="currentColor" opacity="0.2" />
        {trendLines.map((t) => {
          const points = data
            .map((d, i) => `${getX(i)},${getY(d[t.key] ?? 0)}`)
            .join(' ')
          return (
            <polyline
              key={t.key}
              points={points}
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeDasharray={t.dash}
            />
          )
        })}
      </svg>
      {/* Legend */}
      <div aria-label="趋势图例" className="flex flex-wrap justify-center gap-6 mt-2">
        {trendLines.map((t) => (
          <span key={t.key} className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <svg aria-hidden="true" width="24" height="6"><line x1="0" y1="3" x2="24" y2="3" stroke="currentColor" strokeWidth="2" strokeDasharray={t.dash} /></svg>
            {t.label}
          </span>
        ))}
      </div>
    </div>
  )
}

export default function CreatorPage() {
  const [summary, setSummary] = useState<CreatorSummary | null>(null)
  const stats = summary?.stats
  const revenue = summary?.revenue
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)
  const [period, setPeriod] = useState(7)
  const [trendData, setTrendData] = useState<TrendPoint[]>([])
  const [hasTrend, setHasTrend] = useState(false)
  const requestSequence = useRef(0)

  const fetchStats = useCallback(async (p?: number) => {
    const requestId = ++requestSequence.current
    setLoading(true)
    setError(null)
    try {
      const r = p === undefined
        ? await api.get('/v1/creator/summary')
        : await api.get('/v1/creator/summary', { params: { period: p } })
      if (requestId !== requestSequence.current) return
      const d: unknown = r.data.data
      if (!isCreatorSummary(d)) throw new Error('Invalid creator summary')
      if (formatCurrency(d.revenue.currency) === null) {
        console.warn('[CreatorPage] currency unavailable in API response', {
          currency: d.revenue.currency,
        })
      }
      setSummary(d)
      setTrendData(Array.isArray(d.stats.trend) ? d.stats.trend : [])
      setHasTrend(Array.isArray(d.stats.trend))
    } catch (err) {
      if (requestId === requestSequence.current) setError(classifyError(err))
    } finally {
      if (requestId === requestSequence.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchStats()
    return () => { requestSequence.current += 1 }
  }, [fetchStats])


  return (
    <div className="max-w-6xl mx-auto">
      <PageHeader eyebrow="CREATOR" title="创作者面板" description="了解内容表现，持续分享你的创作。" />
      {loading ? (
        <StatsSkeleton />
      ) : error ? (
        <ErrorState error={error} onRetry={() => fetchStats(hasTrend ? period : undefined)} />
      ) : !stats || !revenue ? (
        <div className="ef-empty">
          <p className="text-muted-foreground mb-4">发布你的第一个视频</p>
          <Link to="/publish" className={buttonVariants()}>去发布</Link>
        </div>
      ) : (
        <>
          <div className="grid grid-cols-2 lg:grid-cols-3 border-t border-l border-border mb-8">
            {metrics.map((m) => (
              <Card key={m.key} className="ef-metric border-0 border-r border-b bg-card">
                <CardContent className="p-5 sm:p-6">
                  <div className="ef-number break-words text-2xl sm:text-4xl font-bold mb-2">{stats[m.key].toLocaleString()}</div>
                  <div className="text-xs text-muted-foreground">{m.label}</div>
                </CardContent>
              </Card>
            ))}
          </div>

          {/* Period filter */}
          {stats.scope === 'own_non_deleted' && <p className="text-sm text-muted-foreground mb-2">包含待审、驳回及下架的未删除作品。</p>}
          {stats.play_basis === 'legacy_detail_views' && <p className="text-sm text-muted-foreground mb-2">播放沿用详情访问次数，收益据此估算。</p>}
          {hasTrend ? <div className="flex flex-wrap gap-2 mb-6">
            {periods.map((p) => (
              <button
                key={p.value}
                onClick={() => { setPeriod(p.value); fetchStats(p.value) }}
                className="ef-tab"
                aria-pressed={period === p.value}
              >
                {p.label}
              </button>
            ))}
          </div> : <p className="text-sm text-muted-foreground mb-6">累计数据</p>}

          {/* Trend chart */}
          {trendData.length > 0 && <section className="border-t border-border py-6"><p className="ef-eyebrow mb-2">// PERFORMANCE</p><h2 className="text-lg font-bold">内容趋势</h2><TrendChart data={trendData} /></section>}

          <div className="grid grid-cols-1 sm:grid-cols-2 border-t border-l border-border mt-6">
            <Card className="ef-metric border-0 border-r border-b">
              <CardContent className="p-6">
                <div className="text-xs text-muted-foreground">视频</div>
                <div className="ef-number text-3xl font-bold">{stats.video_count}</div>
              </CardContent>
            </Card>
            <Card className="ef-metric border-0 border-r border-b">
              <CardContent className="p-6">
                <div className="text-xs text-muted-foreground">预估收益</div>
                {(() => {
                  const symbol = formatCurrency(revenue.currency)
                  if (symbol === null) {
                    return <>
                      <div className="ef-number text-3xl font-bold">{revenue.estimated_revenue.toFixed(2)}</div>
                      <p className="text-sm text-muted-foreground">币种不可用</p>
                    </>
                  }
                  return (
                    <div className="ef-number text-3xl font-bold">
                      {symbol}{revenue.estimated_revenue.toFixed(2)}
                    </div>
                  )
                })()}
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </div>
  )
}
