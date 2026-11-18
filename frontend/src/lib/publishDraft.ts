import type { UploadResult } from '../api/upload'

export interface PublishDraft {
  version: 2; user_id: string; draft_id: 'active'
  title: string; description: string; tags: string; music: string
  updated_at: string; local_revision: number; phase: 'editing' | 'uploaded' | 'publishing'
  upload_ref?: UploadResult
  publish_request_id?: string; publish_payload?: string
}
const operations = new Map<string, Promise<unknown>>()
const key = (uid: string) => `feedvideo:publish:v2:${uid}:active`
const deletedKey = (uid: string) => `${key(uid)}:deleted`
export class DraftConflictError extends Error {
  constructor() { super('Draft conflict: the saved draft has changed'); this.name = 'DraftConflictError' }
}
interface DraftTombstone { version: 2; user_id: string; draft_id: 'active'; deleted_at: string; deleted_snapshot: PublishDraft | null }
function valid(d: any): d is PublishDraft {
  return d?.version === 2 && typeof d.user_id === 'string' && /^[1-9]\d*$/.test(d.user_id) && BigInt(d.user_id) <= 9223372036854775807n && d.draft_id === 'active' &&
    ['title', 'description', 'tags', 'music'].every(k => typeof d[k] === 'string') &&
    ['editing', 'uploaded', 'publishing'].includes(d.phase) && Number.isSafeInteger(d.local_revision) && d.local_revision >= 0 &&
    Number.isFinite(Date.parse(d.updated_at)) && new TextEncoder().encode(JSON.stringify(d)).length <= 65536
}
function serial<T>(uid: string, operation: () => Promise<T>): Promise<T> {
  const previous = operations.get(uid) ?? Promise.resolve()
  const result = previous.catch(() => {}).then(operation)
  operations.set(uid, result)
  void result.then(() => { if (operations.get(uid) === result) operations.delete(uid) }, () => { if (operations.get(uid) === result) operations.delete(uid) })
  return result
}
function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (typeof indexedDB === 'undefined') { reject(new Error('IndexedDB unavailable')); return }
    const request = indexedDB.open('feedvideo-publish-drafts', 2)
    let done = false
    const timeout = setTimeout(() => { done = true; reject(new Error('IndexedDB blocked')) }, 2000)
    request.onupgradeneeded = () => { if (!request.result.objectStoreNames.contains('drafts')) request.result.createObjectStore('drafts', { keyPath: ['user_id', 'draft_id'] }) }
    request.onerror = () => { clearTimeout(timeout); done = true; reject(request.error) }
    request.onblocked = () => { clearTimeout(timeout); done = true; reject(new Error('IndexedDB blocked')) }
    request.onsuccess = () => { clearTimeout(timeout); if (done) request.result.close(); else { done = true; resolve(request.result) } }
  })
}
async function idb<T>(mode: IDBTransactionMode, execute: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await openDB()
  return new Promise((resolve, reject) => {
    const tx = db.transaction('drafts', mode), request = execute(tx.objectStore('drafts'))
    let result: T
    const timer = setTimeout(() => { tx.abort() }, 2000)
    request.onsuccess = () => { result = request.result }
    tx.oncomplete = () => { clearTimeout(timer); db.close(); resolve(result) }
    tx.onerror = tx.onabort = () => { clearTimeout(timer); db.close(); reject(tx.error ?? new Error('Draft transaction failed')) }
  })
}
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)
function tombstone(value: any, uid: string): value is DraftTombstone {
  return value?.version === 2 && value.user_id === uid && value.draft_id === 'active' && Number.isFinite(Date.parse(value.deleted_at)) &&
    (value.deleted_snapshot === null || (valid(value.deleted_snapshot) && value.deleted_snapshot.user_id === uid))
}
function fallbackRecord(uid: string): { raw: string | null; value: unknown } {
  const raw = localStorage.getItem(key(uid))
  let value: unknown
  if (raw && raw.length <= 70000) { try { value = JSON.parse(raw) } catch { /* Corrupt records cannot authorize an overwrite. */ } }
  return { raw, value }
}
function sameIntent(a: PublishDraft, b: PublishDraft) {
  return typeof a.publish_request_id === 'string' && !!a.publish_request_id && typeof a.publish_payload === 'string' && !!a.publish_payload &&
    a.publish_request_id === b.publish_request_id && a.publish_payload === b.publish_payload
}
function selectDraft(uid: string, values: unknown[]): PublishDraft | null {
  const deletions = values.filter(value => tombstone(value, uid)) as DraftTombstone[]
  const candidates = values.filter(value => valid(value) && value.user_id === uid && Date.parse(value.updated_at) >= Date.now() - 30 * 86400000) as PublishDraft[]
  const remaining = candidates.filter(draft => !deletions.some(deletion => same(deletion.deleted_snapshot, draft)))
  const publishing = remaining.filter(draft => draft.phase === 'publishing')
  if (publishing.length > 1 && !sameIntent(publishing[0], publishing[1])) throw new DraftConflictError()
  const preferred = publishing.length ? publishing : remaining
  preferred.sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at) || b.local_revision - a.local_revision)
  return preferred[0] ?? null
}
function checkChange(uid: string, values: unknown[], next: PublishDraft | null, expected: PublishDraft | null | undefined): PublishDraft | null {
  const current = selectDraft(uid, values)
  if (expected !== undefined) {
    if (!same(current, expected)) throw new DraftConflictError()
  } else if (current?.phase === 'publishing' && (!next || next.phase !== 'publishing' || !sameIntent(current, next))) {
    throw new DraftConflictError()
  } else if (!current && next?.phase === 'publishing' && values.some(value => tombstone(value, uid))) {
    throw new DraftConflictError()
  }
  return current
}
async function changeIDB(uid: string, next: PublishDraft | null, expected: PublishDraft | null | undefined): Promise<void> {
  const db = await openDB()
  return new Promise((resolve, reject) => {
    const tx = db.transaction('drafts', 'readwrite'), store = tx.objectStore('drafts'), request = store.get([uid, 'active'])
    let failure: unknown, observedFallback: { raw: string | null; value: unknown } | undefined
    const timer = setTimeout(() => tx.abort(), 2000)
    request.onsuccess = () => {
      try {
        try { observedFallback = fallbackRecord(uid) } catch { /* IDB can remain available without localStorage. */ }
        const current = checkChange(uid, [request.result, observedFallback?.value], next, expected)
        const value: PublishDraft | DraftTombstone = next ?? { version: 2, user_id: uid, draft_id: 'active', deleted_at: new Date().toISOString(), deleted_snapshot: current }
        store.put(value)
      } catch (error) { failure = error; tx.abort() }
    }
    tx.oncomplete = () => {
      clearTimeout(timer); db.close()
      // Do not remove a fallback that changed while the IDB transaction ran.
      try { if (observedFallback && localStorage.getItem(key(uid)) === observedFallback.raw) localStorage.removeItem(key(uid)) } catch { /* Read reconciles retained records. */ }
      try { localStorage.removeItem(deletedKey(uid)) } catch { /* Primary tombstones remain durable. */ }
      resolve()
    }
    tx.onerror = tx.onabort = () => { clearTimeout(timer); db.close(); reject(failure ?? tx.error ?? new Error('Draft transaction failed')) }
  })
}
async function read(uid: string): Promise<PublishDraft | null> {
  let persistent: unknown, fallback: unknown
  try { persistent = await idb('readonly', store => store.get([uid, 'active'])) } catch { /* Use browser fallback. */ }
  try { fallback = fallbackRecord(uid).value } catch { /* IDB remains available without localStorage. */ }
  const draft = selectDraft(uid, [persistent, fallback])
  // Old timestamp-only deletions remain compatible; new deletions are scoped
  // to the exact removed record and cannot hide a different publishing intent.
  try { const deleted = Date.parse(localStorage.getItem(deletedKey(uid)) ?? ''); if (draft && Number.isFinite(deleted) && Date.parse(draft.updated_at) <= deleted) return null } catch { /* Primary still available. */ }
  return draft
}
export function loadPublishDraft(uid: string): Promise<PublishDraft | null> { return serial(uid, () => read(uid)) }
export function savePublishDraft(draft: PublishDraft, expected?: PublishDraft | null): Promise<void> {
  if (!valid(draft)) return Promise.reject(new Error('Invalid or oversized draft'))
  // Snapshot now: callers can change fields before the queued transaction runs.
  const snapshot = structuredClone(draft)
  const prior = expected === undefined ? undefined : structuredClone(expected)
  return serial(draft.user_id, async () => {
    try { await changeIDB(snapshot.user_id, snapshot, prior) }
    catch (error) {
      // A failed available primary cannot authorize writes against an empty
      // fallback: it may still contain a protected publish we could not read.
      if (error instanceof DraftConflictError || typeof indexedDB !== 'undefined') throw error
      const stored = fallbackRecord(snapshot.user_id)
      checkChange(snapshot.user_id, [stored.value], snapshot, prior)
      localStorage.setItem(key(snapshot.user_id), JSON.stringify(snapshot))
      localStorage.removeItem(deletedKey(snapshot.user_id))
    }
  })
}
export function deletePublishDraft(uid: string, expected?: PublishDraft | null): Promise<void> {
  const prior = expected === undefined ? undefined : structuredClone(expected)
  return serial(uid, async () => {
    try { await changeIDB(uid, null, prior) }
    catch (error) {
      if (error instanceof DraftConflictError || typeof indexedDB !== 'undefined') throw error
      const stored = fallbackRecord(uid)
      const current = checkChange(uid, [stored.value], null, prior)
      const deletion: DraftTombstone = { version: 2, user_id: uid, draft_id: 'active', deleted_at: new Date().toISOString(), deleted_snapshot: current }
      localStorage.setItem(key(uid), JSON.stringify(deletion))
      localStorage.removeItem(deletedKey(uid))
    }
    try { sessionStorage.removeItem(`publish-draft:${uid}:v1`) } catch { /* Durable tombstone is already confirmed. */ }
  })
}
export async function migratePublishDraft(uid: string): Promise<PublishDraft | null> {
  const existing = await loadPublishDraft(uid)
  if (existing) return existing
  const legacyKey = `publish-draft:${uid}:v1`
  let legacy: any
  try { legacy = JSON.parse(sessionStorage.getItem(legacyKey) ?? 'null') } catch { return null }
  if (!legacy || !['title', 'description', 'tags', 'music'].some(k => typeof legacy[k] === 'string' && legacy[k])) return null
  const draft: PublishDraft = { version: 2, user_id: uid, draft_id: 'active', title: typeof legacy.title === 'string' ? legacy.title : '', description: typeof legacy.description === 'string' ? legacy.description : '', tags: typeof legacy.tags === 'string' ? legacy.tags : '', music: typeof legacy.music === 'string' ? legacy.music : '', updated_at: new Date().toISOString(), local_revision: 0, phase: 'editing' }
  await savePublishDraft(draft, null)
  const confirmed = await loadPublishDraft(uid)
  if (!confirmed || JSON.stringify(confirmed) !== JSON.stringify(draft)) throw new Error('Draft migration not confirmed')
  sessionStorage.removeItem(legacyKey)
  return confirmed
}
