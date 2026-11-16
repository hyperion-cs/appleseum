import { computed, ref } from 'vue'
import { en, ru, type Strings } from './strings.ts'

export const languages = {
  en: { strings: en, locale: 'en-US', name: 'English' },
  ru: { strings: ru, locale: 'ru-RU', name: 'Русский' },
} as const
export type Language = keyof typeof languages

const codes = Object.keys(languages) as Language[]

export const language = ref<Language>('en')
export const nextLanguage = computed(
  () => codes[(codes.indexOf(language.value) + 1) % codes.length]!,
)
export const t = computed<Strings>(() => languages[language.value].strings)

export function errorText(code: string) {
  return t.value.errors[code as keyof Strings['errors']] ?? t.value.error
}

export function useErrorMessage() {
  const code = ref('')
  const message = computed(() => (code.value ? errorText(code.value) : ''))
  return { code, message }
}
