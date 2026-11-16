const defaultCacheCapacity = 16

export function createCache<T>(capacity = defaultCacheCapacity) {
  const values = new Map<string, T>()
  return {
    get: (key: string) => values.get(key),

    has: (key: string) => values.has(key),

    set(key: string, value: T) {
      values.delete(key)
      values.set(key, value)
      if (values.size > capacity) values.delete(values.keys().next().value!)
    },

    clear(prefix = '') {
      for (const key of values.keys()) if (key.startsWith(prefix)) values.delete(key)
    },
  }
}

// Clearing pages aborts in-flight reads and prevents stale responses from refilling the cache.
export function createPageCache<T>(
  fetchPage: (key: string, signal: AbortSignal) => Promise<T>,
  capacity = defaultCacheCapacity,
) {
  const cache = createCache<T>(capacity)
  const pending = new Map<string, { request: Promise<T>; abort: AbortController }>()

  function read(key: string, fresh = false): Promise<T> {
    const running = pending.get(key)
    if (running) return running.request
    if (!fresh && cache.has(key)) return Promise.resolve(cache.get(key)!)
    const abort = new AbortController()
    const request = fetchPage(key, abort.signal)
      .then((page) => {
        if (pending.get(key)?.request === request) {
          cache.set(key, page)
        }
        return page
      })
      .finally(() => {
        if (pending.get(key)?.request === request) pending.delete(key)
      })
    pending.set(key, { request, abort })
    return request
  }

  return {
    read,

    prefetch(keys: Iterable<string>) {
      for (const key of keys) void read(key).catch(() => {})
    },

    peek: (key: string) => cache.get(key),

    clear(prefix = '') {
      cache.clear(prefix)
      for (const [key, entry] of pending) {
        if (!key.startsWith(prefix)) continue
        pending.delete(key)
        entry.abort.abort()
      }
    },
  }
}
