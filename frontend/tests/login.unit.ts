import { test } from 'node:test'
import assert from 'node:assert/strict'
import { loginFailure } from '../src/composables/login.ts'

test('rejected password never opens 2FA; rejected code retains its challenge; expiry resets it', () => {
  const form = { password: 'password', code: '', twoFactor: false, errorCode: '' }

  function receive(code: string) {
    const failure = loginFailure(code)
    form.twoFactor = failure.twoFactor
    form.errorCode = failure.errorCode
    form.code = ''
    if (!failure.keepPassword) form.password = ''
  }

  receive('password_rejected')
  assert.equal(form.twoFactor, false)
  assert.equal(form.password, '')
  assert.equal(form.errorCode, 'password_rejected')
  form.password = 'correct-password'
  receive('two_factor_required')
  assert.equal(form.twoFactor, true)
  assert.equal(form.password, 'correct-password')
  form.code = '111111'
  receive('verification_code_rejected')
  assert.equal(form.twoFactor, true)
  assert.equal(form.password, 'correct-password')
  assert.equal(form.code, '')
  assert.equal(form.errorCode, 'verification_code_rejected')
  receive('verification_expired')
  assert.equal(form.twoFactor, false)
  assert.equal(form.password, '')
  assert.equal(form.errorCode, 'verification_expired')
  for (const code of [
    'account_credentials_rejected',
    'apple_device_verification_failed',
    'apple_response_unexpected',
    'apple_unavailable',
    'login_processing_failed',
    'error',
  ]) {
    receive('two_factor_required')
    receive(code)
    assert.equal(form.twoFactor, false)
    assert.equal(form.errorCode, code)
  }
})
