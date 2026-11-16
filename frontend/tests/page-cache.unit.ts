import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createCache, createPageCache } from '../src/api/pageCache.ts'
import { adjacentPageQueries } from '../src/api/pagination.ts'

test('a mutation starts a new request; an older result cannot refill or remove it', async () => {
  const finish: ((page: number) => void)[] = []
  const pages = createPageCache(() => new Promise<number>((resolve) => finish.push(resolve)), 2)
  const before = pages.read('catalog')
  assert.equal(pages.read('catalog'), before)
  pages.clear()
  const after = pages.read('catalog')
  assert.equal(finish.length, 2)
  finish[0]!(1)
  assert.equal(await before, 1)
  assert.equal(pages.peek('catalog'), undefined)
  assert.equal(pages.read('catalog'), after)
  finish[1]!(2)
  assert.equal(await after, 2)
  assert.equal(pages.peek('catalog'), 2)
})

test('capacity is bounded and a failed request can be retried', async () => {
  let calls = 0
  const pages = createPageCache(async (key: string) => {
    calls++
    if (calls === 1) throw new Error('offline')
    return key
  }, 2)
  await assert.rejects(pages.read('a'), /offline/)
  assert.equal(await pages.read('a'), 'a')
  await pages.read('b')
  assert.equal(await pages.read('a'), 'a')
  assert.equal(calls, 3)
  await pages.read('a', true)
  await pages.read('c')
  assert.equal(pages.peek('b'), undefined)
  assert.equal(pages.peek('a'), 'a')
  assert.equal(pages.peek('c'), 'c')
})

test('clearing one resource preserves cached and in-flight pages of another', async () => {
  let finish: (page: string) => void = () => {}

  const pages = createPageCache(
    (key: string) =>
      key === 'library?next'
        ? new Promise<string>((resolve) => (finish = resolve))
        : Promise.resolve(key),
    4,
  )
  await pages.read('library?first')
  await pages.read('catalog?first')
  const pending = pages.read('library?next')
  pages.clear('catalog?')
  assert.equal(pages.peek('catalog?first'), undefined)
  assert.equal(pages.peek('library?first'), 'library?first')
  assert.equal(pages.read('library?next'), pending)
  finish('next')
  await pending
  assert.equal(pages.peek('library?next'), 'next')
})

test('invalidation cancels obsolete reads without aborting another resource', async () => {
  const signals = new Map<string, AbortSignal>()
  const finish = new Map<string, (value: string) => void>()
  const pages = createPageCache((key, signal) => {
    signals.set(key, signal)
    return new Promise<string>((resolve) => finish.set(key, resolve))
  }, 2)
  const catalog = pages.read('catalog?first'),
    library = pages.read('library?first')
  pages.clear('catalog?')
  assert.equal(signals.get('catalog?first')!.aborted, true)
  assert.equal(signals.get('library?first')!.aborted, false)
  finish.get('catalog?first')!('obsolete')
  finish.get('library?first')!('current')
  await Promise.all([catalog, library])
  assert.equal(pages.peek('catalog?first'), undefined)
  assert.equal(pages.peek('library?first'), 'current')
})

test('value caches bound history pages and clear only their requested prefix', () => {
  const pages = createCache<number>(2)
  pages.set('versions:first', 0)
  pages.set('versions:second', 2)
  pages.set('versions:first', 1)
  pages.set('library:first', 3)
  assert.equal(pages.has('versions:second'), false)
  assert.equal(pages.get('versions:first'), 1)
  pages.clear('versions:')
  assert.equal(pages.has('versions:first'), false)
  assert.equal(pages.get('library:first'), 3)
})

test('neighbour prefetch preserves filters and navigation reuses the in-flight request', async () => {
  type Page = { next: boolean; previous: boolean; items: { cursor: string }[] }
  const requests: string[] = []

  let finish: (value: Page) => void = () => {}

  const pages = createPageCache<Page>((query) => {
    requests.push(query)
    return new Promise((resolve) => {
      finish = resolve
    })
  })
  const first = { next: true, previous: false, items: [{ cursor: 'next-token' }] }
  pages.prefetch(adjacentPageQueries('limit=5&source=Apple&source=API&q=login', first))
  assert.deepEqual(requests, [
    'limit=5&source=Apple&source=API&q=login&cursor=next-token&move=next',
  ])
  const pending = pages.read(requests[0]!, true)
  assert.equal(requests.length, 1)
  const result = { next: false, previous: true, items: [] }
  finish(result)
  assert.equal(await pending, result)
  assert.equal(pages.peek(requests[0]!), result)
  assert.equal(await pages.read(requests[0]!), result)
  assert.equal(requests.length, 1)
})
