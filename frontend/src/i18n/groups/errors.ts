import { maxSelectionSize } from '../../api/types.ts'

// Messages for API and browser error codes.
export const ru = {
  errors: {
    installation_no_manifest:
      'Устройство пока не запросило данные установки. Возможно, установка не началась.',
    installation_no_ipa: 'Манифест запрошен, но скачивание приложения пока не началось.',
    installation_interrupted: 'Скачивание прервалось. Ожидаем повторного подключения.',
    installation_manifest_failed: 'Не удалось передать манифест. Можно повторить установку.',
    installation_status_unavailable:
      'Не удаётся получить состояние скачивания. Установка может продолжаться.',
    installation_status_expired: 'Данные о скачивании больше недоступны.',
    copy_failed: 'Не удалось скопировать ссылку. Повторите попытку.',

    invalid_ipa: 'Не удалось прочитать приложение. Выберите корректный файл .ipa.',
    import_too_large: 'Файл превышает лимит 4 ГиБ.',
    import_busy: 'Сервер добавляет другие IPA. Повторите попытку чуть позже.',
    import_failed: 'Не удалось добавить IPA. Повторите попытку.',
    reauthentication_failed: 'Повторный вход не восстановил доступ к Apple. Войдите заново.',
    profile_authentication_required:
      'Apple не разрешила обновить профиль. Показаны сохранённые сведения. Для обновления выйдите и войдите снова.',
    profile_unavailable:
      'Не удалось обновить профиль у Apple. Показаны сохранённые сведения. Попробуйте ещё раз.',
    purchase_history_authentication_required:
      'Apple не разрешила прочитать историю покупок. Для обновления списка выйдите и войдите снова. Сессия и сохранённый каталог оставлены.',
    invalid_device_settings: 'Проверьте модель, версию macOS, локаль и часовой пояс.',
    device_authentication_required: 'Войдите в аккаунт, чтобы изменить сохранённое устройство.',
    read_only: 'Эта загрузка принадлежит другому аккаунту.',
    operation_unavailable: 'Действие недоступно.',
    session_key_required: 'Не удалось восстановить вход. Войдите заново.',
    browser_storage_unavailable: 'Разрешите хранение данных этого сайта в браузере.',
    https_required: 'Откройте Appleseum по HTTPS.',
    action_unavailable: 'Дождитесь завершения скачивания выбранных приложений.',
    invalid_request: 'Проверьте введённые данные.',
    invalid_credentials: 'Укажите почту или телефон, пароль и корректный код подтверждения.',
    login_failed: 'Не удалось завершить вход. Подробности — в журнале сервера.',
    two_factor_required: 'Введите код подтверждения.',
    account_credentials_rejected:
      'Apple не приняла данные для входа. Проверьте их и повторите попытку.',
    password_rejected: 'Введите корректный пароль для этого аккаунта Apple и повторите попытку.',
    verification_code_rejected:
      'Apple не приняла код подтверждения. Проверьте код и введите его снова.',
    verification_expired: 'Проверку входа нужно начать заново. Введите Apple ID и пароль.',
    apple_device_verification_failed:
      'Apple не удалось проверить устройство для входа. Это не означает, что пароль неверный.',
    apple_response_unexpected:
      'Не удалось завершить вход: неожиданный ответ Apple. Подробности — в журнале сервера.',
    login_processing_failed: 'Не удалось обработать данные входа. Подробности — в журнале сервера.',
    sign_in_required: 'Войдите в Apple ID.',
    session_expired: 'Apple не принимает сессию. Войдите снова.',
    apple_unavailable: 'Не удалось связаться с App Store. Попробуйте ещё раз.',
    apple_sign_in_denied:
      'Apple отклонил запрос входа (HTTP 403). Это не означает, что данные для входа введены неверно. Попробуйте позже или подключите сервер к другой сети.',
    storage_full:
      'На сервере недостаточно свободного места. Освободите место; очередь продолжится автоматически.',
    queue_full:
      'Очередь заполнена. Выберите меньше приложений или дождитесь завершения скачиваний.',
    file_too_large: 'Файл превышает лимит 4 ГиБ.',
    download_timeout: 'Скачивание не завершилось за 30 минут.',
    storage_failed: 'Не удалось сохранить данные на сервере.',
    server_unavailable: 'Сервер останавливается или перегружен. Попробуйте чуть позже.',
    busy: 'Дождитесь завершения текущего действия.',
    sharing_active: 'Раздача уже идёт. Остановите её перед запуском новой.',
    sharing_limit: 'Можно запустить не больше 8 раздач. Остановите одну из активных.',
    sharing_duplicate: 'Раздача с таким набором файлов уже запущена.',
    delivery_verification_failed:
      'Не удалось подтвердить подлинность элемента доставки. Используйте два устройства: одно для раздачи, другое для установки.',
    shared_files:
      'Некоторые выбранные файлы используются в раздачах. Сначала остановите эти раздачи.',
    rate_limited: 'Слишком много попыток. Повторите немного позже.',
    version_unavailable: 'Версия недоступна',
    versions_changed: 'Список версий обновился. Загрузите его заново.',
    download_failed: 'Не удалось скачать приложение. Попробуйте снова.',
    interrupted: 'Скачивание прервано перезапуском сервера.',
    canceled: 'Скачивание отменено.',
    invalid_package: 'Не удалось подготовить приложение к установке.',
    missing_package: 'Файл приложения не найден. Скачайте его заново.',
    invalid_selection: 'Выбранные приложения больше недоступны. Обновите список.',
    selection_too_large: `Можно выбрать не более ${maxSelectionSize} записей. Уточните фильтры или выберите меньше.`,
    files_in_use: 'Файлы сейчас передаются. Повторите удаление позже.',
    not_found: 'Запись больше недоступна.',
  },
}

export const en: typeof ru = {
  errors: {
    installation_no_manifest:
      'Your device has not requested installation data yet. Installation may not have started.',
    installation_no_ipa: 'The manifest was requested, but the app download has not started yet.',
    installation_interrupted: 'The download was interrupted. Waiting for reconnection.',
    installation_manifest_failed: 'Could not send the manifest. You can try installing again.',
    installation_status_unavailable:
      'Download status is unavailable. Installation may still be in progress.',
    installation_status_expired: 'Download activity is no longer available.',
    copy_failed: 'Could not copy the link. Please try again.',

    invalid_ipa: 'Could not read this app. Choose a valid .ipa file.',
    import_too_large: 'This file exceeds the 4 GiB limit.',
    import_busy: 'The server is adding other IPAs. Try again shortly.',
    import_failed: 'Could not add this IPA. Please try again.',
    reauthentication_failed:
      'Signing in again did not restore access to Apple. Please sign in manually.',
    profile_authentication_required:
      'Apple denied the profile update. Saved details are shown. Sign out and back in to refresh them.',
    profile_unavailable:
      'Could not update your profile from Apple. Saved details are shown. Please try again.',
    purchase_history_authentication_required:
      'Apple denied access to purchase history. Sign out and back in to refresh the list. Your session and cached catalog have been kept.',
    invalid_device_settings: 'Check the model, macOS version, locale and time zone.',
    device_authentication_required: 'Sign in to change the saved device.',
    read_only: 'This download belongs to another account.',
    operation_unavailable: 'This action is unavailable.',
    session_key_required: 'Your sign-in could not be restored. Sign in again.',
    browser_storage_unavailable: 'Allow this site to store data in your browser.',
    https_required: 'Open Appleseum over HTTPS.',
    action_unavailable: 'Wait for the selected apps to finish downloading.',
    invalid_request: 'Check the entered values.',
    invalid_credentials:
      'Enter your email or phone number, password and a valid verification code.',
    login_failed: 'Could not complete sign-in. See the server log for details.',
    two_factor_required: 'Enter your verification code.',
    account_credentials_rejected:
      'Apple did not accept your credentials. Check them and try again.',
    password_rejected: 'Enter the correct password for this Apple Account and try again.',
    verification_code_rejected:
      'Apple did not accept the verification code. Check it and enter it again.',
    verification_expired: 'Start sign-in verification again. Enter your Apple ID and password.',
    apple_device_verification_failed:
      'Apple could not verify the device for sign-in. This does not mean the password is incorrect.',
    apple_response_unexpected:
      'Could not complete sign-in: unexpected response from Apple. See the server log for details.',
    login_processing_failed: 'Could not process sign-in data. See the server log for details.',
    sign_in_required: 'Sign in to Apple ID.',
    session_expired: 'Apple no longer accepts this session. Please sign in again.',
    apple_unavailable: 'Could not connect to App Store. Please try again.',
    apple_sign_in_denied:
      'Apple refused the sign-in request (HTTP 403). This does not mean your sign-in details are incorrect. Try later or connect the server to another network.',
    storage_full:
      'The server is out of free space. Free some space; the queue will resume automatically.',
    queue_full: 'The queue is full. Select fewer apps or wait for downloads to finish.',
    file_too_large: 'The file exceeds the 4 GiB limit.',
    download_timeout: 'The download did not finish within 30 minutes.',
    storage_failed: 'Could not save data on this server.',
    server_unavailable: 'The server is shutting down or busy. Try again shortly.',
    busy: 'Wait for the current action to finish.',
    sharing_active: 'Hosting is already active. Stop it before starting again.',
    sharing_limit: 'You can host up to 8 sets of files. Stop an existing session first.',
    sharing_duplicate: 'The same set of files is already being hosted.',
    delivery_verification_failed:
      'A delivery component could not be authenticated. Use two devices: one to host apps and another to install them.',
    shared_files: 'Some selected files are being hosted. Stop those hosting sessions first.',
    rate_limited: 'Too many attempts. Try again shortly.',
    version_unavailable: 'Version unavailable',
    versions_changed: 'The version list has changed. Load it again.',
    download_failed: 'Could not download this app. Please try again.',
    interrupted: 'Download was interrupted when the server restarted.',
    canceled: 'Download canceled.',
    invalid_package: 'Could not prepare this app for installation.',
    missing_package: 'The app file is missing. Download it again.',
    invalid_selection: 'Selected apps are no longer available. Refresh the list.',
    selection_too_large: `Select up to ${maxSelectionSize} items. Narrow the filters or select fewer items.`,
    files_in_use: 'Apps are being installed. Try deleting them later.',
    not_found: 'This item is no longer available.',
  },
}
