export const defaultPageSize = 10
export const defaultVersionPageSize = 5

export type PageDirection = 'previous' | 'next'
type CursorItem = { cursor?: string }
export type CursorPage<T extends CursorItem = CursorItem> = {
  items: T[]
  previous: boolean
  next: boolean
}

export function pageCursor(page: { items: CursorItem[] }, direction: PageDirection) {
  return (direction === 'previous' ? page.items[0] : page.items.at(-1))?.cursor ?? ''
}

export function pageMove(direction: PageDirection) {
  return direction === 'previous' ? 'prev' : 'next'
}

// Keep the server's row cursors while fitting an already loaded page locally.
export function shortenPage<P extends CursorPage>(page: P, size: number): P {
  if (size < 1 || size >= page.items.length || !page.items[size - 1]?.cursor) return page
  return { ...page, items: page.items.slice(0, size), next: true }
}

export function adjacentPageQueries(query: string, page: CursorPage) {
  return (['previous', 'next'] as const)
    .filter((direction) => page[direction])
    .map((direction) => {
      const params = new URLSearchParams(query)
      params.set('cursor', pageCursor(page, direction))
      params.set('move', pageMove(direction))
      return params.toString()
    })
}
