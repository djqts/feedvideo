import type { WatchEvent } from './watchQueue'

interface PlaybackOptions {
  videoID: string; contentVersion: number
  emit: (event: WatchEvent) => void; flush: () => void
  clock?: () => number
}

/** Translate media events into continuous foreground intervals, never seek deltas. */
export function attachPlaybackTelemetry(video: HTMLVideoElement, options: PlaybackOptions) {
  const clock = options.clock ?? (() => performance.now())
  let session = crypto.randomUUID(), seq = 0, ended = false, playing = false, started = false, stopped = false
  let visible = false, impression = false, exposureTimer: ReturnType<typeof setTimeout> | undefined
  let lastWall = clock(), lastPosition = video.currentTime * 1000
  let start = lastPosition, end = lastPosition, watched = 0, rate = video.playbackRate
  const foreground = () => document.visibilityState !== 'hidden'
  const position = () => Math.max(0, Math.round(video.currentTime * 1000))
  const emit = (type: string, delta = 0, from = position(), to = from) => {
    if (stopped) return
    options.emit({ schema_version: 1, event_id: crypto.randomUUID(), type, occurred_at: new Date().toISOString(), payload: {
      video_id: options.videoID, content_version: options.contentVersion, playback_session_id: session, seq: seq++,
      position_ms: delta ? Math.round(to) : position(), watched_delta_ms: Math.round(delta),
      interval_start_ms: Math.round(from), interval_end_ms: Math.round(to), playback_rate: rate,
      visible: delta ? true : visible, foreground: delta ? true : foreground(),
    } })
  }
  const finish = () => { if (watched >= 1 && end > start) emit('behavior.watch_progress', watched, start, end); watched = 0; start = end = position() }
  const reset = () => { lastWall = clock(); lastPosition = video.currentTime * 1000; start = end = lastPosition; rate = video.playbackRate }
  const sample = () => {
    const now = clock(), currentPosition = video.currentTime * 1000, wall = now - lastWall, media = currentPosition - lastPosition
    if (playing && visible && foreground() && wall > 0 && wall <= 1500 && media > 0 && rate >= 0.25 && rate <= 4 && video.playbackRate === rate && Math.abs(media - wall * rate) <= 500) {
      if (watched > 0 && Math.abs(currentPosition - start - (watched + wall) * rate) > 500) finish()
      if (watched === 0) start = lastPosition
      end = currentPosition; watched += wall
      if (watched >= 5000) finish()
    } else if (wall > 0 && (media > 0 || wall > 1500)) finish()
    lastWall = now; lastPosition = currentPosition
  }
  const exposure = () => {
    if (exposureTimer) clearTimeout(exposureTimer)
    if (!impression && visible && foreground()) exposureTimer = setTimeout(() => {
      if (visible && foreground() && !impression) { impression = true; emit('behavior.impression') }
    }, 1000)
  }
  const onPlaying = () => {
    if (ended) { session = crypto.randomUUID(); seq = 0; started = false; impression = false; ended = false }
    if (playing) return
    playing = true; reset()
    if (!started) { emit('behavior.play_start'); started = true }
    exposure()
  }
  const onPause = () => { sample(); finish(); playing = false; emit('behavior.pause') }
  const onWaiting = () => { sample(); finish(); playing = false }
  const onSeeking = () => { finish(); playing = false; reset() }
  const onRate = () => { finish(); reset() }
  const onEnded = () => { sample(); finish(); playing = false; ended = true; emit('behavior.complete'); options.flush() }
  const onVisibility = () => { finish(); reset(); exposure() }
  const observer = typeof IntersectionObserver !== 'undefined' ? new IntersectionObserver(entries => {
    for (const entry of entries) if (entry.target === video) {
      const next = entry.intersectionRatio >= 0.5
      if (next !== visible) { finish(); visible = next; reset(); exposure() }
    }
  }, { threshold: [0, 0.5, 1] }) : undefined
  observer?.observe(video)
  // Without IntersectionObserver only count a measurable visible element.
  if (!observer) { const rect = video.getBoundingClientRect(); visible = rect.width > 0 && rect.height > 0 && rect.top >= 0 && rect.bottom <= window.innerHeight; exposure() }
  const handlers: [string, EventListener][] = [['playing', onPlaying], ['pause', onPause], ['waiting', onWaiting], ['seeking', onSeeking], ['ratechange', onRate], ['ended', onEnded], ['timeupdate', sample]]
  for (const [name, handler] of handlers) video.addEventListener(name, handler)
  document.addEventListener('visibilitychange', onVisibility)
  const timer = setInterval(sample, 250)
  return { stop() {
    if (stopped) return
    sample(); finish(); if (started && !ended) emit('behavior.skip'); options.flush(); stopped = true
    clearInterval(timer); if (exposureTimer) clearTimeout(exposureTimer); observer?.disconnect()
    for (const [name, handler] of handlers) video.removeEventListener(name, handler)
    document.removeEventListener('visibilitychange', onVisibility)
  } }
}
