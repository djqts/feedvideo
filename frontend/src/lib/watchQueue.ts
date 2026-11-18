export interface WatchEvent {
  schema_version: 1
  event_id: string
  type: string
  occurred_at: string
  payload: {
    video_id: string; content_version: number; playback_session_id: string; seq: number
    position_ms: number; watched_delta_ms: number; interval_start_ms: number; interval_end_ms: number
    playback_rate: number; visible: boolean; foreground: boolean
  }
}
export interface WatchAck { event_id: string; status: 'accepted' | 'duplicate' | 'rejected' | 'retryable'; code?: string }
type WatchSender = (events: WatchEvent[], signal: AbortSignal) => Promise<WatchAck[]>
const retention = 7 * 24 * 60 * 60 * 1000

/** One active account queue. Server event identity makes retries idempotent. */
export class WatchQueue {
  private items: WatchEvent[] = []
  private timer: ReturnType<typeof setTimeout> | undefined
  private inFlight: Promise<void> | undefined
  private abort: AbortController | undefined
  private stopped = false
  private failures = 0
  dropped = 0
  rejected = 0
  private key: string

  constructor(uid: string, private send: WatchSender, private current: () => boolean) {
    this.key = `feedvideo:watch:v1:${uid}`
    try {
      const raw = localStorage.getItem(this.key)
      if (raw && raw.length <= 1_000_000) {
        const parsed: unknown = JSON.parse(raw)
        if (Array.isArray(parsed)) this.items = parsed.filter(e => this.valid(e)).slice(0, 500)
      }
    } catch { /* Storage unavailable: keep a bounded memory queue. */ }
    this.prune(); this.schedule(5000)
  }
  get pending(): number { return this.items.length }
  private valid(e: any): e is WatchEvent {
    return e?.schema_version === 1 && typeof e.event_id === 'string' && e.event_id.length === 36 &&
      typeof e.type === 'string' && e.type.startsWith('behavior.') && typeof e.occurred_at === 'string' &&
      Number.isFinite(Date.parse(e.occurred_at)) && typeof e.payload?.video_id === 'string' &&
      typeof e.payload.playback_session_id === 'string' && Number.isSafeInteger(e.payload.seq) &&
      new TextEncoder().encode(JSON.stringify(e)).length <= 4096
  }
  private save(): void {
    try { localStorage.setItem(this.key, JSON.stringify(this.items)) } catch { /* Memory fallback. */ }
  }
  private prune(): void {
    const now = Date.now()
    this.items = this.items.filter(e => Date.parse(e.occurred_at) >= now - retention && Date.parse(e.occurred_at) <= now + 300000)
    this.save()
  }
  enqueue(event: WatchEvent): void {
    if (this.stopped || !this.current() || !this.valid(event)) return
    this.prune()
    if (this.items.some(e => e.event_id === event.event_id)) return
    if (this.items.length >= 500) { this.dropped++; return }
    this.items.push(structuredClone(event)); this.save()
    if (this.items.length >= 20 && this.failures === 0) void this.flush()
  }
  private schedule(delay: number): void {
    if (this.timer) clearTimeout(this.timer)
    if (!this.stopped) this.timer = setTimeout(() => { this.timer = undefined; void this.flush() }, delay)
  }
  flush(): Promise<void> {
    if (this.inFlight) return this.inFlight
    if (this.stopped || !this.current()) return Promise.resolve()
    this.prune()
    if (!this.items.length) { this.schedule(5000); return Promise.resolve() }
    if (this.timer) clearTimeout(this.timer)
    const batch: WatchEvent[] = []
    for (const event of this.items.slice(0, 50)) {
      if (new TextEncoder().encode(JSON.stringify({ events: [...batch, event] })).length > 65536) break
      batch.push(event)
    }
    this.abort = new AbortController()
    const signal = this.abort.signal
    this.inFlight = (async () => {
      try {
        const acknowledgments = await this.send(structuredClone(batch), signal)
        if (!this.current() || this.stopped || signal.aborted) return
        const confirmed = new Set<string>()
        if (Array.isArray(acknowledgments)) {
          const sent = new Set(batch.map(e => e.event_id))
          for (const ack of acknowledgments) if (sent.has(ack?.event_id) && ['accepted', 'duplicate', 'rejected'].includes(ack.status)) {
            confirmed.add(ack.event_id); if (ack.status === 'rejected') this.rejected++
          }
        }
        this.items = this.items.filter(e => !confirmed.has(e.event_id)); this.save()
        this.failures = confirmed.size === batch.length ? 0 : Math.min(5, this.failures + 1)
      } catch { this.failures = Math.min(5, this.failures + 1) }
      finally { this.inFlight = undefined; this.abort = undefined; this.schedule(Math.min(60000, 5000 * 2 ** Math.max(0, this.failures - 1))) }
    })()
    return this.inFlight
  }
  stop(): void { this.stopped = true; if (this.timer) clearTimeout(this.timer); this.abort?.abort() }
}
