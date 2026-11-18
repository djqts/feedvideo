import api from './client'
import type { WatchAck, WatchEvent } from '../lib/watchQueue'

export async function ingestWatchEvents(events: WatchEvent[], signal: AbortSignal): Promise<WatchAck[]> {
  // No implicit refresh replay: an old account's telemetry must never be
  // reissued with a new account cookie. The scoped queue retries original IDs.
  const result = await api.post('/v1/behavior/events', { events }, { signal, skipAuthRefresh: true, timeout: 10000 })
  return result.data?.data?.results ?? []
}
