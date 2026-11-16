// Virtual device settings.
export const ru = {
  settingsLoading: 'Загрузка настроек…',
  deviceTitle: 'Устройство Apple',
  deviceDescription:
    'Профиль виртуального устройства для входа и запросов к Apple. Создаётся автоматически; обычно менять ничего не нужно.',
  deviceIdentity: 'Идентификаторы и совместимость',
  deviceAuthenticationRequired: 'Войдите в аккаунт, чтобы изменить сохранённое устройство.',
  deviceSessionsEnd: 'Все сессии этого устройства будут завершены.',
  deviceModel: 'Модель',
  deviceSoftware: 'Клиент',
  deviceOs: 'Версия macOS',
  deviceOsHelp:
    'Версия macOS виртуального устройства. Список зависит от модели; номер сборки подставляется автоматически. Можно менять без выхода: идентификаторы устройства и сессия сохраняются.',
  deviceId: 'ID устройства',
  deviceIdHelp:
    'Идентификатор виртуального устройства, от имени которого утилита обращается к Apple. Создаётся автоматически и сохраняется между запусками; меняется при смене модели или кнопкой «Создать новое устройство».',
  deviceGuidHelp:
    'Короткий идентификатор того же виртуального устройства для запросов и скачиваний. Вычисляется из ID устройства и меняется вместе с ним.',
  deviceModelHelp:
    'Модель виртуального устройства, которую утилита сообщает Apple. В списке — ноутбуки с поддержкой Configurator 2.21, от новых к старым. Смена модели создаёт новое устройство после подтверждения.',
  deviceSoftwareHelp:
    'Версия Configurator фиксирована: по его протоколу утилита обращается к Apple.',
  deviceLocale: 'Локаль',
  deviceLocaleHelp:
    'Язык и регион виртуального устройства в формате BCP 47. Не меняет регион App Store вашего аккаунта.',
  deviceTimeZone: 'Часовой пояс',
  deviceTimeZoneHelp: 'Часовой пояс, который утилита передаёт в Apple.',
  saveSettings: 'Сохранить',
  deviceDefaults: 'Вернуть штатные настройки',
  deviceDefaultsConfirm: 'Будут восстановлены штатные настройки.',
  deviceRestore: 'Вернуть',
  deviceDefaultsHint: 'Смена модели создаёт новое устройство. Каталог и скачанные IPA сохраняются.',
  deviceModelChange: 'Сменить модель устройства?',
  deviceChange: 'Изменить',
  deviceModelChangeConfirm:
    'Смена модели создаст новые идентификаторы устройства и удалит данные проверки запросов. При следующем входе Apple может повторно запросить код двухфакторной аутентификации. Каталог и скачанные IPA сохранятся.',
  deviceNew: 'Создать новое устройство',
  deviceCreate: 'Создать',
  deviceNewConfirm:
    'ID устройства и GUID будут заменены, данные IdMS и AMD удалены. Apple распознает новое устройство: если включена двухфакторная аутентификация, при следующем входе потребуется снова ввести код подтверждения. Настройки языка и региона, скачанные IPA и каталог сохранятся.',
}

export const en: typeof ru = {
  settingsLoading: 'Loading settings…',
  deviceTitle: 'Apple device',
  deviceDescription:
    'A virtual device profile for signing in and contacting Apple. Created automatically; you usually do not need to change it.',
  deviceIdentity: 'Identifiers and compatibility',
  deviceAuthenticationRequired: 'Sign in to change the saved device.',
  deviceSessionsEnd: 'All sessions using this device will be signed out.',
  deviceModel: 'Model',
  deviceSoftware: 'Client',
  deviceOs: 'macOS version',
  deviceOsHelp:
    'The virtual device’s macOS version. Available versions depend on the model; the build number is filled in automatically. Editable without signing out: device identifiers and the session are kept.',
  deviceId: 'Device ID',
  deviceIdHelp:
    'Identifies the virtual device this utility presents to Apple. Generated automatically and kept across restarts; replaced when changing the model or using “Create a new device”.',
  deviceGuidHelp:
    'A short identifier for the same virtual device, used for requests and downloads. Derived from the device ID and changes with it.',
  deviceModelHelp:
    'The virtual device model reported to Apple. The list includes notebooks supporting Configurator 2.21, from newest to oldest. Changing the model creates a new device after confirmation.',
  deviceSoftwareHelp:
    'The Configurator version is fixed: this utility uses its protocol to communicate with Apple.',
  deviceLocale: 'Locale',
  deviceLocaleHelp:
    'Language and region of the virtual device, expressed as a BCP 47 tag. Does not change your account’s App Store region.',
  deviceTimeZone: 'Time zone',
  deviceTimeZoneHelp: 'Time zone reported to Apple.',
  saveSettings: 'Save',
  deviceDefaults: 'Restore default settings',
  deviceDefaultsConfirm: 'Default settings will be restored.',
  deviceRestore: 'Restore',
  deviceDefaultsHint:
    'Changing the model creates a new device. The catalog and downloaded IPAs are kept.',
  deviceModelChange: 'Change the device model?',
  deviceChange: 'Change',
  deviceModelChangeConfirm:
    'Changing the model will create new device identifiers and delete request verification data. Apple may request a two-factor authentication code again at your next sign-in. The catalog and downloaded IPAs will be kept.',
  deviceNew: 'Create a new device',
  deviceCreate: 'Create',
  deviceNewConfirm:
    'The device ID and GUID will be replaced and IdMS and AMD data deleted. Apple will recognize a new device: if two-factor authentication is enabled, you will need to enter a verification code again at your next sign-in. Language and region settings, downloaded IPAs and the catalog will be kept.',
}
