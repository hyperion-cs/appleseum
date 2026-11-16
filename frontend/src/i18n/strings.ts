import * as common from './groups/common.ts'
import * as login from './groups/login.ts'
import * as catalog from './groups/catalog.ts'
import * as library from './groups/library.ts'
import * as shares from './groups/shares.ts'
import * as settings from './groups/settings.ts'
import * as profile from './groups/profile.ts'
import * as debug from './groups/debug.ts'
import * as errors from './groups/errors.ts'

// A group per screen keeps parallel changes apart; keys are unique across groups.
export const ru = {
  ...common.ru,
  ...login.ru,
  ...catalog.ru,
  ...library.ru,
  ...shares.ru,
  ...settings.ru,
  ...profile.ru,
  ...debug.ru,
  ...errors.ru,
}
export type Strings = typeof ru
export const en: Strings = {
  ...common.en,
  ...login.en,
  ...catalog.en,
  ...library.en,
  ...shares.en,
  ...settings.en,
  ...profile.en,
  ...debug.en,
  ...errors.en,
}
