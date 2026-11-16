import { test } from 'node:test'
import assert from 'node:assert/strict'
import { language, useErrorMessage } from '../src/i18n/index.ts'

test('session error follows language loaded after the error and later language changes', () => {
  const previous = language.value
  try {
    language.value = 'en'
    const { code, message } = useErrorMessage()
    code.value = 'session_expired'
    assert.equal(message.value, 'Apple no longer accepts this session. Please sign in again.')
    language.value = 'ru'
    assert.equal(message.value, 'Apple не принимает сессию. Войдите снова.')
    language.value = 'en'
    assert.equal(message.value, 'Apple no longer accepts this session. Please sign in again.')
    code.value = ''
    assert.equal(message.value, '')
  } finally {
    language.value = previous
  }
})

test('purchase-history rejection is scoped to history in both languages', () => {
  const previous = language.value
  try {
    const { code, message } = useErrorMessage()
    code.value = 'purchase_history_authentication_required'
    language.value = 'en'
    assert.match(message.value, /purchase history/)
    assert.match(message.value, /session and cached catalog have been kept/)
    language.value = 'ru'
    assert.match(message.value, /историю покупок/)
    assert.match(message.value, /Сессия и сохранённый каталог оставлены/)
  } finally {
    language.value = previous
  }
})

test('password rejection asks for a corrected password in both languages', () => {
  const previous = language.value
  try {
    const { code, message } = useErrorMessage()
    code.value = 'password_rejected'
    language.value = 'ru'
    assert.equal(
      message.value,
      'Введите корректный пароль для этого аккаунта Apple и повторите попытку.',
    )
    language.value = 'en'
    assert.equal(message.value, 'Enter the correct password for this Apple Account and try again.')
    code.value = 'account_credentials_rejected'
    assert.equal(message.value, 'Apple did not accept your credentials. Check them and try again.')
    language.value = 'ru'
    assert.equal(
      message.value,
      'Apple не приняла данные для входа. Проверьте их и повторите попытку.',
    )
  } finally {
    language.value = previous
  }
})

test('cell formatting follows locale changes and malformed dates cannot break rendering', async () => {
  const { bytes, date, purchaseDate, percent } = await import('../src/i18n/format.ts')
  const previous = language.value
  try {
    for (const lang of ['en', 'ru'] as const) {
      language.value = lang
      const locale = lang === 'ru' ? 'ru-RU' : 'en-US'
      assert.equal(
        bytes(1_250_000),
        new Intl.NumberFormat(locale, {
          style: 'unit',
          unit: 'megabyte',
          maximumFractionDigits: 1,
        }).format(1.25),
      )
      assert.equal(
        bytes(1_500_000_000),
        new Intl.NumberFormat(locale, {
          style: 'unit',
          unit: 'gigabyte',
          maximumFractionDigits: 1,
        }).format(1.5),
      )
      assert.equal(date('invalid date'), '—')
      assert.equal(purchaseDate(''), '—')
      assert.equal(purchaseDate('unrecognized date'), 'unrecognized date')
      assert.equal(
        percent(12.5),
        new Intl.NumberFormat(locale, {
          style: 'percent',
          minimumFractionDigits: 1,
          maximumFractionDigits: 1,
        })
          .format(0.125)
          .replace(/\s/gu, ''),
      )
    }
  } finally {
    language.value = previous
  }
})

test('string groups never define the same key twice', async () => {
  const { readdirSync } = await import('node:fs')
  const directory = new URL('../src/i18n/groups/', import.meta.url)
  const owners = new Map<string, string>()
  for (const file of readdirSync(directory)) {
    const group: { ru: object; en: object } = await import(new URL(file, directory).href)
    assert.deepEqual(Object.keys(group.en).sort(), Object.keys(group.ru).sort(), file)
    for (const key of Object.keys(group.ru)) {
      assert.equal(owners.get(key), undefined, `${key} is in ${owners.get(key)} and ${file}`)
      owners.set(key, file)
    }
  }
})
