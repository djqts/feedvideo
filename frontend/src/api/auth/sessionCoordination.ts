import { AuthGenerationChangedError } from './errors'
import { parseAuthUserID } from './parse'

interface StoragePort { getItem(key: string): string | null; setItem(key: string, value: string): void; removeItem(key: string): void }
interface LockPort { request<T>(name: string, options: { signal: AbortSignal }, callback: () => Promise<T>): Promise<T> }
interface ChannelPort {
  postMessage(message: unknown): void
  addEventListener(type: 'message', listener: (event: { data: unknown }) => void): void
  removeEventListener(type: 'message', listener: (event: { data: unknown }) => void): void
  close(): void
}
interface StorageEventPort {
  addEventListener(type: 'storage', listener: (event: { key: string | null; storageArea: StoragePort | null }) => void): void
  removeEventListener(type: 'storage', listener: (event: { key: string | null; storageArea: StoragePort | null }) => void): void
}
interface Options {
  storage?: StoragePort
  locks?: LockPort
  channel?: () => ChannelPort
  storageEvents?: StorageEventPort
  now?: () => number
  pause?: (ms: number) => Promise<void>
}
export interface SessionScope { revision: string; receipt: string; invalidated: boolean }
export interface SessionNotice { type: 'refreshed' | 'invalidated' | 'changed'; revision: string }
const REVISION_KEY = 'feedvideo:session-revision:v1'
const RECEIPT_KEY = 'feedvideo:refresh-receipt:v1'
const LEASE_KEY = 'feedvideo:refresh-lease:v1'
const INVALIDATED_KEY = 'feedvideo:session-invalidated:v1'
const BUDGET = 35000
const validID = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value)
const uniqueID = () => crypto.randomUUID()
const timeout = () => Object.assign(new Error('刷新协调超时，请重试'), { code: 'ETIMEDOUT' })

/** Same-origin coordination is an optimization; cookies and authorization stay server-owned. */
export function createSessionCoordination(options: Options = {}) {
  const now = options.now ?? Date.now
  const pause = options.pause ?? (ms => new Promise(resolve => setTimeout(resolve, ms)))
  let localRevision = 'initial', localReceipt = ''
  let localOnly = false
  let localInvalidated = false
  let channel: ChannelPort | undefined
  let listeningStorage = false
  const subscribers = new Set<(notice: SessionNotice) => void>()
  function read(key: string): string | null | undefined {
    try { return options.storage?.getItem(key) } catch { return undefined }
  }
  function write(key: string, value: string): boolean {
    try { if (!options.storage) return false; options.storage.setItem(key, value); return true } catch { return false }
  }
  function remove(key: string) { try { options.storage?.removeItem(key) } catch { /* Memory remains usable. */ } }
  function parse(raw: string | null | undefined): Record<string, unknown> {
    if (!raw || raw.length > 1024) return {}
    try { const value = JSON.parse(raw); return value && typeof value === 'object' ? value : {} } catch { return {} }
  }
  function capture(): SessionScope {
    const rawRevision = read(REVISION_KEY)
    const revision = localOnly || rawRevision === undefined ? localRevision : validID(rawRevision) ? rawRevision : 'initial'
    const rawReceipt = read(RECEIPT_KEY), stored = parse(rawReceipt)
    const receipt = localOnly || rawReceipt === undefined ? localReceipt : stored.revision === revision && validID(stored.id) ? stored.id : ''
    const invalidated = localOnly || rawRevision === undefined ? localInvalidated : read(INVALIDATED_KEY) === revision
    return { revision, receipt, invalidated }
  }
  const current = (scope: SessionScope) => scope.revision === capture().revision
  function check(scope: SessionScope) {
    if (!current(scope)) throw new AuthGenerationChangedError({ generation: 0, source: 'cross-tab session', timestamp: now(), retryable: false }, undefined, 1, 0)
  }
  let observed = capture()
  let confirmedRevision: string | undefined = observed.invalidated ? undefined : observed.revision
  let actor: string | undefined
  let anonymousRevision: string | undefined
  function synchronize() {
    const latest = capture()
    const identityChanged = latest.revision !== observed.revision || latest.invalidated !== observed.invalidated
    if (!identityChanged && latest.receipt === observed.receipt) return
    const type: SessionNotice['type'] = identityChanged ? latest.invalidated ? 'invalidated' : 'changed' : 'refreshed'
    if (identityChanged) { confirmedRevision = undefined; anonymousRevision = undefined; actor = undefined }
    observed = latest
    for (const listener of subscribers) listener({ type, revision: latest.revision })
  }
  function identityReady() {
    const latest = capture()
    return !latest.invalidated && confirmedRevision === latest.revision
  }
  function anonymousReady() { return anonymousRevision === capture().revision }
  function confirmedActor(): string | undefined {
    return anonymousReady() ? 'anonymous' : identityReady() ? actor : undefined
  }
  function confirmIdentity(scope: SessionScope, uid?: string) {
    synchronize()
    const latest = capture()
    const exactUID = parseAuthUserID(uid)
    if (uid !== undefined && !exactUID) return
    if (!scope.invalidated && !latest.invalidated && scope.revision === latest.revision) {
      confirmedRevision = latest.revision; anonymousRevision = undefined; actor = exactUID
    }
  }
  function confirmAnonymous(scope: SessionScope) {
    synchronize()
    const latest = capture()
    if (scope.revision === latest.revision) { anonymousRevision = latest.revision; confirmedRevision = undefined; actor = undefined }
  }
  function rejectIdentity(scope: SessionScope, expectedActor?: string) {
    synchronize()
    if (!current(scope) || confirmedActor() !== expectedActor || (!identityReady() && !anonymousReady())) return
    confirmedRevision = undefined; anonymousRevision = undefined; actor = undefined
    // No shared storage is required. Notify only this tab; a mismatch does
    // not revoke the peer's server session or clear its shared cookies.
    for (const listener of subscribers) listener({ type: 'changed', revision: capture().revision })
  }
  const receive = (event: { data: unknown }) => {
    const message = event.data as Partial<SessionNotice> | null
    if (!message || typeof message !== 'object' || Object.keys(message).length !== 2 || !validID(message.revision) || !['refreshed', 'invalidated', 'changed'].includes(message.type ?? '')) return
    const latest = capture()
    if (message.revision !== latest.revision) return
    synchronize()
  }
  const receiveStorage = (event: { key: string | null; storageArea: StoragePort | null }) => {
    if (event.storageArea !== options.storage || (event.key !== null && ![REVISION_KEY, RECEIPT_KEY, INVALIDATED_KEY].includes(event.key))) return
    synchronize()
  }
  function ensureChannel() {
    if (!channel && options.channel) {
      try { channel = options.channel(); channel.addEventListener('message', receive) } catch { /* Lock/storage still coordinate. */ }
    }
  }
  function broadcast(type: SessionNotice['type']) {
    observed = capture(); ensureChannel()
    try { channel?.postMessage({ type, revision: observed.revision } satisfies SessionNotice) } catch { /* Receipts remain authoritative. */ }
    if (subscribers.size === 0) closeChannel()
  }
  function invalidate() {
    confirmedRevision = undefined; anonymousRevision = undefined; actor = undefined
    localRevision = uniqueID(); localReceipt = ''; localInvalidated = true
    if (!write(INVALIDATED_KEY, localRevision)) localOnly = true
    if (!write(REVISION_KEY, localRevision)) localOnly = true
    remove(RECEIPT_KEY)
    broadcast('invalidated')
  }
  function activate() {
    confirmedRevision = undefined; anonymousRevision = undefined; actor = undefined
    localRevision = uniqueID(); localReceipt = ''; localInvalidated = false
    if (!write(REVISION_KEY, localRevision)) localOnly = true
    remove(INVALIDATED_KEY); remove(RECEIPT_KEY)
    broadcast('changed')
  }
  async function withFallback<T>(scope: SessionScope, task: (owned: () => boolean) => Promise<T>): Promise<T> {
    if (read(LEASE_KEY) === undefined) return task(() => true)
    const owner = uniqueID(), deadline = now() + BUDGET
    while (now() < deadline) {
      check(scope)
      const lease = parse(read(LEASE_KEY))
      if (typeof lease.expires !== 'number' || lease.expires <= now() || !validID(lease.owner)) {
        if (!write(LEASE_KEY, JSON.stringify({ owner, expires: now() + BUDGET }))) return task(() => true)
        await pause(20)
        const owned = () => { const saved = parse(read(LEASE_KEY)); return saved.owner === owner && typeof saved.expires === 'number' && saved.expires > now() }
        if (owned()) {
          try { return await task(owned) }
          finally { if (parse(read(LEASE_KEY)).owner === owner) remove(LEASE_KEY) }
        }
      }
      await pause(50)
    }
    throw timeout()
  }
  async function refresh(scope: SessionScope, rotate: () => Promise<void>, verify: () => Promise<void>): Promise<void> {
    check(scope)
    if (capture().invalidated) throw new AuthGenerationChangedError({ generation: 0, source: 'invalidated session', timestamp: now(), retryable: false }, undefined, 1, 0)
    const task = async (owned: () => boolean) => {
      check(scope)
      if (capture().receipt !== scope.receipt) {
        await verify(); check(scope)
        return
      }
      await rotate(); check(scope)
      if (!owned()) throw timeout()
      localReceipt = uniqueID()
      write(RECEIPT_KEY, JSON.stringify({ id: localReceipt, revision: scope.revision }))
      broadcast('refreshed')
    }
    if (options.locks) {
      const abort = new AbortController()
      const timer = setTimeout(() => abort.abort(), BUDGET)
      try { await options.locks.request('feedvideo:auth-refresh:v1', { signal: abort.signal }, () => task(() => true)) }
      catch (error) { if (abort.signal.aborted) throw timeout(); throw error }
      finally { clearTimeout(timer) }
    } else await withFallback(scope, task)
  }
  function subscribe(listener: (notice: SessionNotice) => void) {
    subscribers.add(listener); ensureChannel()
    if (!listeningStorage && options.storageEvents) {
      try { options.storageEvents.addEventListener('storage', receiveStorage); listeningStorage = true } catch { /* Synchronous reads remain authoritative. */ }
    }
    return () => { subscribers.delete(listener); if (subscribers.size === 0) closeListeners() }
  }
  function closeChannel() {
    if (channel) { channel.removeEventListener('message', receive); channel.close(); channel = undefined }
  }
  function closeListeners() {
    closeChannel()
    if (listeningStorage) { options.storageEvents?.removeEventListener('storage', receiveStorage); listeningStorage = false }
  }
  function dispose() { subscribers.clear(); closeListeners() }
  return { capture, current, synchronize, identityReady, anonymousReady, confirmedActor, confirmIdentity, confirmAnonymous, rejectIdentity, invalidate, activate, refresh, subscribe, dispose }
}

export function createBrowserSessionCoordination() {
  let storage: StoragePort | undefined
  try { storage = window.localStorage } catch { /* Current-tab fallback. */ }
  return createSessionCoordination({ storage, storageEvents: window, locks: navigator.locks,
    channel: typeof window.BroadcastChannel === 'function' ? () => new window.BroadcastChannel('feedvideo:auth-session:v1') : undefined,
  })
}
