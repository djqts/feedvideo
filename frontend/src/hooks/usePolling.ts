import { useEffect, useRef } from 'react'

/**
 * Runs `fn` every `intervalMs`, but ONLY while the tab is visible.
 * Uses recursive setTimeout (NOT setInterval) to prevent request
 * stacking when network latency exceeds intervalMs.
 * When the tab is hidden the timer is cleared; when it becomes visible
 * again `fn` fires immediately and the timer resumes.
 * Pass `enabled=false` to disable entirely (e.g. logged out).
 */
export function usePolling(fn: () => void, intervalMs: number, enabled = true) {
  const saved = useRef(fn)
  useEffect(() => { saved.current = fn }, [fn])

  useEffect(() => {
    if (!enabled) return
    let timerId: ReturnType<typeof setTimeout> | null = null
    let stopped = false

    const tick = async () => {
      try { await saved.current() } catch {}
      if (!stopped && document.visibilityState === 'visible') {
        timerId = setTimeout(tick, intervalMs)
      }
    }
    const start = () => {
      stopped = false
      tick() // fire immediately on (re)start
    }
    const stop = () => {
      stopped = true
      if (timerId) { clearTimeout(timerId); timerId = null }
    }
    const onVisibility = () => {
      if (document.visibilityState === 'visible') start()
      else stop()
    }

    if (document.visibilityState === 'visible') start()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [intervalMs, enabled])
}
