import { useEffect, useRef } from 'react'
import { checkCollected, setCollected } from '../api/collect'
import { classifyError, type ClassifiedError } from './classifyError'

export interface CollectFact {
  collected: boolean
  collect_count: number
  version: string
}

export async function commitCollectIntent(videoId: string, desired: boolean, version?: string, isCurrent = () => true): Promise<CollectFact> {
  try {
    const response = await setCollected(videoId, desired, version)
    return response.data.data
  } catch (error) {
    if ((error as { response?: { status?: number } })?.response?.status !== 409) throw error
    if (!isCurrent()) throw error
    const current = await checkCollected(videoId)
    if (!isCurrent()) throw error
    const response = await setCollected(videoId, desired, current.data.data.version)
    return response.data.data
  }
}

export function collectIntentError(error: unknown): ClassifiedError {
  const classified = classifyError(error)
  const status = (error as { response?: { status?: number } })?.response?.status
  const message = status === 409 ? '收藏状态发生变化，请重新读取后再操作'
    : classified.type === 'forbidden' ? '没有权限修改收藏'
    : classified.type === 'notfound' ? '视频已不可用，无法收藏'
    : classified.type === 'network' || classified.type === 'timeout' ? '收藏结果暂时无法确认，请重试'
    : classified.type === 'server' ? '收藏服务暂时不可用，请重试'
    : classified.message
  // This domain retry reads the current version on conflict before the next
  // PUT. It does not enable blind replay for generic HTTP 409 errors.
  return { ...classified, message, retriable: status === 409 || classified.retriable }
}

interface IntentEntry {
  collected: boolean
  version?: string
  desired?: boolean
  token?: symbol
}

/** Each mounted page owns its locks and confirmed facts; scope changes invalidate all work. */
export function useCollectIntents(scope: string) {
  const stateRef = useRef({ scope, generation: 0, entries: new Map<string, IntentEntry>() })
  if (stateRef.current.scope !== scope) {
    stateRef.current = { scope, generation: stateRef.current.generation + 1, entries: new Map() }
  }
  useEffect(() => () => { stateRef.current.generation++ }, [])
  const getGeneration = () => stateRef.current.generation
  const isCurrent = (generation: number) => generation === stateRef.current.generation
  const run = async (videoId: string, collected: boolean, onSuccess: (fact: CollectFact) => void, onError: (error: ClassifiedError) => void, fixedDesired?: boolean) => {
    const state = stateRef.current
    const generation = state.generation
    let entry = state.entries.get(videoId)
    if (!entry) {
      entry = { collected }
      state.entries.set(videoId, entry)
    }
    if (entry.token) return
    const token = Symbol(videoId)
    entry.token = token
    const desired = entry.desired ?? fixedDesired ?? !entry.collected
    entry.desired = desired
    try {
      const fact = await commitCollectIntent(videoId, desired, entry.version, () => state === stateRef.current && isCurrent(generation))
      if (state !== stateRef.current || !isCurrent(generation)) return
      entry.collected = fact.collected
      entry.version = fact.version
      entry.desired = undefined
      onSuccess(fact)
    } catch (error) {
      if (state === stateRef.current && isCurrent(generation)) onError(collectIntentError(error))
    } finally {
      if (state === stateRef.current && isCurrent(generation) && entry.token === token) entry.token = undefined
    }
  }
  return { run, getGeneration, isCurrent }
}
