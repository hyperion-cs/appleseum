import { productTagline, productTaglineRu } from '../../api/types.ts'

// Apple sign-in and verification.
export const ru = {
  title: productTaglineRu,
  intro:
    'Даже те, которые уже недоступны в App Store — устанавливайте их на iPhone/iPad, без кабеля.',
  signingIn: 'Выполняется вход',
  signInTitle: 'Вход',
  signInText: 'Введите данные для входа в аккаунт Apple.',
  loginIdentifier: 'Почта или телефон',
  password: 'Пароль',
  showPassword: 'Показать пароль',
  hidePassword: 'Скрыть пароль',
  code: 'Код подтверждения',
  twoFactor: 'Введите код с доверенного устройства Apple.',
  codeHelp: 'Код не пришёл?',
  codeHelpCredentialsTitle: 'Запрос кода принят',
  codeHelpCredentials:
    'Apple приняла запрос кода после проверки входа. Если уведомление задерживается, получите код на доверенном устройстве.',
  codeHelpOfflineTitle: 'Получите код без интернета',
  codeHelpOfflineDevice:
    'Если уведомление Apple задерживается, можно получить код вручную. Нужно доверенное устройство с iOS 18 / iPadOS 18 или новее, на котором выполнен вход в тот же аккаунт Apple.',
  codeHelpOfflineDisconnect: 'Включите авиарежим и отдельно отключите Wi-Fi.',
  codeHelpOfflineSettings: 'Откройте «Настройки» → ваше имя → «Вход и безопасность».',
  codeHelpOfflineGenerate:
    'В окне «Данные учётной записи недоступны» нажмите «Получить код проверки» и запомните шестизначный код.',
  codeHelpOfflineReturn:
    'Выключите авиарежим и включите Wi-Fi. Вернитесь в эту форму и введите полученный код.',
  retryLogin: 'Начать заново',
  rememberMe: 'Запомнить меня',
}

export const en: typeof ru = {
  title: productTagline,
  intro: 'Even apps delisted from the App Store — install them on iPhone/iPad without a cable.',
  signingIn: 'Signing in',
  signInTitle: 'Sign in',
  signInText: 'Enter your Apple account sign-in details.',
  loginIdentifier: 'Email or phone number',
  password: 'Password',
  showPassword: 'Show password',
  hidePassword: 'Hide password',
  code: 'Verification code',
  twoFactor: 'Enter the code from your trusted Apple device.',
  codeHelp: 'Didn’t get a code?',
  codeHelpCredentialsTitle: 'Code request accepted',
  codeHelpCredentials:
    'Apple accepted the code request after sign-in verification. If the notification is delayed, get a code on your trusted device.',
  codeHelpOfflineTitle: 'Get a code offline',
  codeHelpOfflineDevice:
    'If the Apple notification is delayed, get a code manually on a trusted iPhone or iPad with iOS 18 / iPadOS 18 or later, already signed in to the same Apple Account.',
  codeHelpOfflineDisconnect: 'Turn on Airplane Mode and separately turn off Wi-Fi.',
  codeHelpOfflineSettings: 'Open Settings → your name → Sign-In & Security.',
  codeHelpOfflineGenerate:
    'In the Account Details Unavailable dialog, tap Get Verification Code and remember the six-digit code.',
  codeHelpOfflineReturn:
    'Turn off Airplane Mode and turn Wi-Fi back on. Return to this form and enter the code.',
  retryLogin: 'Start over',
  rememberMe: 'Remember me',
}
