import './httpEnvironment.ts'
import assert from 'node:assert/strict'
import { test, type TestContext } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { useInstallation } from '../src/composables/useInstallation.ts'
import { installationLink } from '../src/platform/installation.ts'
import type { InstallationProgress } from '../src/api/types.ts'
import { createTestRenderer } from './sfc.ts'

const source =
  'itms-services://?action=download-manifest&url=' +
  encodeURIComponent('https://host.test/manifests/share.plist')
const at = (stage: InstallationProgress['stage'], percent = 0): InstallationProgress => ({
  stage,
  percent,
  skipped: 0,
})

function mount(t: TestContext) {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let installation!: ReturnType<typeof useInstallation>
  const app = createTestRenderer().createApp({
    setup() {
      installation = useInstallation(() => 'share')
      return () => null
    },
  })
  app.mount({})
  let mounted = true
  const unmount = () => {
    if (mounted) app.unmount()
    mounted = false
  }
  t.after(unmount)
  return { installation, unmount }
}

function trace(link: string) {
  return new URL(new URL(link).searchParams.get('url')!).searchParams.get('trace_id')!
}

test('install links put the trace inside the encoded manifest URL', () => {
  const link = new URL(installationLink(source, 'abc1234'))
  assert.equal(link.searchParams.get('action'), 'download-manifest')
  assert.equal(link.searchParams.has('trace_id'), false)
  const manifest = new URL(link.searchParams.get('url')!)
  assert.equal(manifest.pathname, '/manifests/share.plist')
  assert.equal(manifest.searchParams.get('trace_id'), 'abc1234')
  assert.equal(new URL(source).searchParams.get('url'), 'https://host.test/manifests/share.plist')
})

test('only explicit installation starts polling; delays and interruptions warn without ending it', async (t) => {
  let status = at('waiting')
  let reads = 0
  t.mock.method(globalThis, 'fetch', async () => {
    reads++
    return Response.json(status)
  })
  const { installation } = mount(t)
  t.mock.timers.tick(20_000)
  await setImmediate()
  assert.equal(reads, 0)
  assert.equal(installation.requested.value, false)
  const link = installation.start(source)
  assert.match(trace(link), /^[a-z0-9]{7}$/)
  await setImmediate()
  t.mock.timers.tick(9_999)
  await setImmediate()
  assert.equal(installation.warning.value, '')
  t.mock.timers.tick(1)
  assert.equal(installation.warning.value, 'installation_no_manifest')
  status = at('manifest')
  t.mock.timers.tick(1_000)
  await setImmediate()
  assert.equal(installation.stage.value, 'manifest')
  assert.equal(installation.warning.value, 'installation_no_ipa')
  status = at('waitingIpa')
  t.mock.timers.tick(1_000)
  await setImmediate()
  assert.equal(installation.stage.value, 'waitingIpa')
  status = at('transferring', 43)
  t.mock.timers.tick(1_000)
  await setImmediate()
  assert.equal(installation.stage.value, 'transferring')
  assert.equal(installation.progress.value?.percent, 43)
  assert.equal(installation.warning.value, '')
  status = at('interrupted', 43)
  t.mock.timers.tick(1_000)
  await setImmediate()
  assert.equal(installation.warning.value, 'installation_interrupted')
  assert.equal(installation.error.value, '')
  // The device reconnects: observation never stopped, so the transfer is seen again.
  status = at('transferring', 60)
  const before = reads
  t.mock.timers.tick(1_000)
  await setImmediate()
  assert.equal(reads, before + 1)
  assert.equal(installation.warning.value, '')
  assert.equal(installation.progress.value?.percent, 60)
})

test('late responses from an earlier attempt or a closed dialog cannot update or restart observation', async (t) => {
  const responses: Array<(value: Response) => void> = []
  t.mock.method(
    globalThis,
    'fetch',
    () => new Promise<Response>((resolve) => responses.push(resolve)),
  )
  const { installation, unmount } = mount(t)
  const first = trace(installation.start(source))
  const second = trace(installation.start(source))
  assert.notEqual(first, second)
  assert.equal(responses.length, 2)
  responses[0]!(Response.json(at('waitingIpa')))
  await setImmediate()
  assert.equal(installation.progress.value, null)
  unmount()
  responses[1]!(Response.json(at('waitingIpa')))
  await setImmediate()
  t.mock.timers.tick(20_000)
  await setImmediate()
  assert.equal(responses.length, 2)
  assert.equal(installation.progress.value, null)
})

test('completion ends polling, and a new attempt resets the ten-second warning', async (t) => {
  let status = at('complete', 100)
  let reads = 0
  t.mock.method(globalThis, 'fetch', async () => {
    reads++
    return Response.json(status)
  })
  const { installation } = mount(t)
  installation.start(source)
  await setImmediate()
  assert.equal(installation.stage.value, 'complete')
  status = at('waiting')
  t.mock.timers.tick(70_000)
  await setImmediate()
  assert.equal(reads, 1)
  assert.equal(installation.stage.value, 'complete')
  installation.start(source)
  await setImmediate()
  assert.equal(installation.stage.value, 'waiting')
  assert.equal(installation.warning.value, '')
  t.mock.timers.tick(10_000)
  await setImmediate()
  assert.equal(installation.warning.value, 'installation_no_manifest')
})

for (const outcome of ['failed', 'expired', 'unavailable'] as const) {
  test(`${outcome}: a terminal outcome ends observation`, async (t) => {
    let status = at('waitingIpa')
    let broken = false
    let reads = 0
    t.mock.method(globalThis, 'fetch', async () => {
      reads++
      return broken
        ? Response.json({ code: 'server_unavailable' }, { status: 503 })
        : Response.json(status)
    })
    const { installation } = mount(t)
    installation.start(source)
    await setImmediate()
    if (outcome === 'failed') status = at('failed')
    if (outcome === 'expired') status = at('waiting')
    if (outcome === 'unavailable') broken = true
    t.mock.timers.tick(1_000)
    await setImmediate()
    if (outcome === 'unavailable') {
      // One or two failed reads are a blip; the third in a row is not.
      assert.equal(installation.error.value, '')
      t.mock.timers.tick(1_000)
      await setImmediate()
      assert.equal(installation.error.value, '')
      t.mock.timers.tick(1_000)
      await setImmediate()
    }
    const expected = {
      failed: 'installation_manifest_failed',
      expired: 'installation_status_expired',
      unavailable: 'installation_status_unavailable',
    }[outcome]
    assert.equal(installation.error.value, expected)
    assert.equal(installation.warning.value, '')
    const before = reads
    t.mock.timers.tick(20_000)
    await setImmediate()
    assert.equal(reads, before)
  })
}

test('a read failure followed by success keeps observing without an error', async (t) => {
  let broken = true
  t.mock.method(globalThis, 'fetch', async () =>
    broken
      ? Response.json({ code: 'server_unavailable' }, { status: 503 })
      : Response.json(at('transferring', 10)),
  )
  const { installation } = mount(t)
  installation.start(source)
  await setImmediate()
  t.mock.timers.tick(1_000)
  await setImmediate()
  broken = false
  t.mock.timers.tick(1_000)
  await setImmediate()
  assert.equal(installation.error.value, '')
  assert.equal(installation.progress.value?.percent, 10)
  broken = true
  for (let i = 0; i < 2; i++) {
    t.mock.timers.tick(1_000)
    await setImmediate()
  }
  assert.equal(installation.error.value, '')
})

test('stopping observation discards pending replies and cancels both timers without closing its scope', async (t) => {
  let finish!: (value: Response) => void
  let reads = 0
  t.mock.method(globalThis, 'fetch', () => {
    reads++
    return new Promise<Response>((resolve) => {
      finish = resolve
    })
  })
  const { installation } = mount(t)
  installation.start(source)
  installation.stop()
  finish(Response.json(at('waitingIpa')))
  await setImmediate()
  t.mock.timers.tick(20_000)
  await setImmediate()
  assert.equal(reads, 1)
  assert.equal(installation.progress.value, null)
  assert.equal(installation.warning.value, '')
  assert.equal(installation.error.value, '')
})
