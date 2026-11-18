/**
 * Shared notification store — single source of truth for notification state.
 *
 * Navbar unread badge reads from this store (NOT from its own polling).
 * NotificationPage reads items + mutations from this store.
 * Only ONE polling owner (Navbar or NotificationPage, whichever is mounted).
 *
 * pendingMutationIds: when a mark-read mutation is in-flight, suspend poll
 * results application.
 * storeEpoch: logout/account switch increments epoch; old epoch responses are
 * discarded silently.
 */

export interface NotificationStoreState {
  items: any[]
  unreadCount: number
  pendingMutationIds: Set<string>
  storeEpoch: number
}

export interface SetItemsOptions {
  epoch?: number
}

export type NotificationStoreListener = () => void

export interface NotificationStoreInstance {
  getState(): NotificationStoreState
  setItems(items: any[], options?: SetItemsOptions): void
  setUnreadCount(count: number, options?: SetItemsOptions): void
  addPendingMutation(id: string): void
  removePendingMutation(id: string): void
  incrementEpoch(): void
  subscribe(listener: NotificationStoreListener): () => void
}

let instance: NotificationStoreInstance | null = null

function createInstance(): NotificationStoreInstance {
  let state: NotificationStoreState = {
    items: [],
    unreadCount: 0,
    pendingMutationIds: new Set(),
    storeEpoch: 0,
  }

  const listeners = new Set<NotificationStoreListener>()

  function notify(): void {
    listeners.forEach((fn) => {
      try {
        fn()
      } catch {
        // isolate listener failures
      }
    })
  }

  function replace(newState: NotificationStoreState): void {
    state = newState
    notify()
  }

  return {
    getState(): NotificationStoreState {
      return state
    },

    setItems(items: any[], options?: SetItemsOptions): void {
      if (options?.epoch !== undefined && options.epoch < state.storeEpoch) {
        return // stale epoch — discard
      }
      replace({ ...state, items })
    },

    setUnreadCount(count: number, options?: SetItemsOptions): void {
      if (options?.epoch !== undefined && options.epoch < state.storeEpoch) {
        return // stale epoch — discard
      }
      replace({ ...state, unreadCount: count })
    },

    addPendingMutation(id: string): void {
      const next = new Set(state.pendingMutationIds)
      next.add(id)
      replace({ ...state, pendingMutationIds: next })
    },

    removePendingMutation(id: string): void {
      const next = new Set(state.pendingMutationIds)
      next.delete(id)
      replace({ ...state, pendingMutationIds: next })
    },

    incrementEpoch(): void {
      replace({
        ...state,
        storeEpoch: state.storeEpoch + 1,
        pendingMutationIds: new Set(), // clear in-flight mutations
      })
    },

    subscribe(listener: NotificationStoreListener): () => void {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

/**
 * Returns the singleton notification store instance.
 */
export function createNotificationStore(): NotificationStoreInstance {
  if (!instance) {
    instance = createInstance()
  }
  return instance
}

/**
 * Clears the singleton — useful in tests.
 */
export function resetNotificationStore(): void {
  instance = null
}
