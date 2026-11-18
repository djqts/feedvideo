import { useEffect, useRef, type RefObject } from 'react'
import { ingestWatchEvents } from '../api/behavior'
import { attachPlaybackTelemetry } from '../lib/playbackTelemetry'
import { WatchQueue } from '../lib/watchQueue'

export function usePlaybackTelemetry(ref: RefObject<HTMLVideoElement>, videoID: string | undefined, contentVersion: number | undefined, uid: string | null, generation: number | undefined) {
  const scope = `${uid ?? ''}:${generation ?? 0}`
  const current = useRef(scope)
  current.current = scope
  useEffect(() => {
    const video = ref.current
    if (!video || !uid || !videoID || !/^[0-9a-f]{24}$/.test(videoID) || !Number.isSafeInteger(contentVersion) || (contentVersion ?? 0) < 1) return
    const boundScope = scope
    const isCurrent = () => current.current === boundScope
    const queue = new WatchQueue(uid, ingestWatchEvents, isCurrent)
    const telemetry = attachPlaybackTelemetry(video, { videoID, contentVersion: contentVersion!, emit: event => queue.enqueue(event), flush: () => { void queue.flush() } })
    const flush = () => { void queue.flush() }
    window.addEventListener('pagehide', flush)
    window.addEventListener('online', flush)
    return () => {
      telemetry.stop()
      window.removeEventListener('pagehide', flush); window.removeEventListener('online', flush)
      // Cancel on every unmount: an unmounted ref can no longer observe a later
      // account switch. The durable queue remains for the original account.
      queue.stop()
    }
  }, [ref, videoID, contentVersion, uid, generation])
}
