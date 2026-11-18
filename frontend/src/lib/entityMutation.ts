/**
 * Mutation versioning for optimistic UI updates.
 *
 * Each entity (video.id for collect, user.id for follow) gets its own store
 * in a module-level Map. The version counter ensures stale server responses
 * are ignored.
 */

export interface EntityState<T> {
  /** What the user last requested. */
  desiredState: T
  /** What the UI shows (matches desiredState after setDesired). */
  displayState: T
  /** What the server confirmed (null until first confirmation). */
  confirmedState: T | null
  /** Sync status: idle | pending | failed. */
  syncStatus: 'idle' | 'pending' | 'failed'
  /** Monotonic per-entity counter — incremented on every setDesired / revert. */
  version: number
}

export interface EntityMutationStore<T> {
  getState(): EntityState<T>
  setDesired(state: T): void
  confirmServer(state: T, version: number): void
  revertToConfirmed(): void
  getSyncStatus(): string
}

const stores = new Map<string, EntityMutationStore<unknown>>()

function makeStore<T>(key: string, initial: T): EntityMutationStore<T> {
  let state: EntityState<T> = {
    desiredState: initial,
    displayState: initial,
    confirmedState: initial,
    syncStatus: 'idle',
    version: 0,
  }

  return {
    getState(): EntityState<T> {
      return state
    },

    setDesired(desired: T): void {
      const nextVersion = state.version + 1
      state = {
        desiredState: desired,
        displayState: desired,
        confirmedState: state.confirmedState,
        syncStatus: 'pending',
        version: nextVersion,
      }
    },

    confirmServer(confirmed: T, version: number): void {
      // Only apply if the version matches — stale responses are ignored.
      if (version !== state.version) return
      state = {
        desiredState: state.desiredState,
        displayState: confirmed,
        confirmedState: confirmed,
        syncStatus: 'idle',
        version: state.version,
      }
    },

    revertToConfirmed(): void {
      // On failure, revert display/desired to last confirmed state.
      const fallback: T = state.confirmedState as T
      const nextVersion = state.version + 1
      state = {
        desiredState: fallback,
        displayState: fallback,
        confirmedState: state.confirmedState,
        syncStatus: 'idle',
        version: nextVersion,
      }
    },

    getSyncStatus(): string {
      return state.syncStatus
    },
  }
}

/**
 * Creates (or retrieves) a mutation store for a given entity key.
 * The same key always returns the same store (singleton per key),
 * enabling FollowDialog and ProfilePage to share the same EntityState.
 */
export function createEntityMutation<T>(initial: T, key: string): EntityMutationStore<T> {
  const existing = stores.get(key)
  if (existing) return existing as EntityMutationStore<T>

  const store = makeStore(key, initial)
  stores.set(key, store)
  return store
}

/**
 * Clears all stores. Useful in tests or on logout/account switch.
 */
export function resetAllEntityStores(): void {
  stores.clear()
}
