import { test } from 'node:test'
import assert from 'node:assert/strict'
import { deviceOs, osCompatibility } from '../src/platform/device.ts'

const iphone = (os: string, browser: string) =>
  `Mozilla/5.0 (iPhone; CPU iPhone OS ${os} like Mac OS X) AppleWebKit/605.1.15 ${browser} Mobile/15E148 Safari/604.1`

test('iOS release comparison handles numeric components and missing patch precision', () => {
  const device = deviceOs(iphone('17_2', 'Version/17.2'), 5)
  assert.equal(osCompatibility('16.10', device), 'compatible')
  assert.equal(osCompatibility('17.2.0', device), 'compatible')
  assert.equal(osCompatibility('17.2.1', device), 'unknown')
  assert.equal(osCompatibility('17.3', device), 'incompatible')
  assert.equal(osCompatibility('18', device), 'incompatible')
  for (const invalid of ['', 'unknown', '17.x', '17.2.1.4']) {
    assert.equal(osCompatibility(invalid, device), 'unknown')
  }
})

test('Safari 26 frozen OS tokens never make compatible iOS releases look incompatible', () => {
  const device = deviceOs(iphone('18_6', 'Version/26.0'), 5)
  assert.equal(osCompatibility('19.0', device), 'compatible')
  assert.equal(osCompatibility('26.0', device), 'compatible')
  assert.equal(osCompatibility('26.1', device), 'unknown')
  assert.equal(osCompatibility('27', device), 'unknown')
})

test('iPad desktop mode is distinguished from macOS and Android', () => {
  const desktop =
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/26.0 Safari/605.1.15'
  assert.equal(osCompatibility('18.0', deviceOs(desktop, 5)), 'compatible')
  assert.equal(deviceOs(desktop, 0), null)
  assert.equal(deviceOs('Mozilla/5.0 (Linux; Android 16) Chrome/140.0', 5), null)
  assert.equal(osCompatibility('16.0', null), 'unknown')
})

test('other iOS browsers use real OS tokens conservatively and tolerate frozen ones', () => {
  const actual = deviceOs(iphone('26_1_0', 'CriOS/140.0'), 5)
  assert.equal(osCompatibility('26.1', actual), 'compatible')
  assert.equal(osCompatibility('26.2', actual), 'incompatible')
  for (const browser of ['CriOS/140.0', 'FxiOS/140.0 Version/18.6']) {
    const frozen = deviceOs(iphone('18_6', browser), 5)
    assert.equal(osCompatibility('18.0', frozen), 'compatible')
    assert.equal(osCompatibility('26.0', frozen), 'unknown')
  }
})
