// Hosting: active shares and installation.
export const ru = {
  searchShares: 'Поиск раздач',
  shareOwner: 'Владелец',
  shareStarted: 'Начало раздачи',
  noShares: 'Раздач пока нет',
  viewShare: 'Посмотреть раздачу',
  confirmStopShare: 'Остановить раздачу?',
  confirmStopShares: 'Остановить выбранные раздачи?',
  moreSharedFiles: (count: number) => `и ещё ${count} шт.`,
  shareTitle: 'Приложения готовы к установке',
  shareSafariRequired: (publicMode: boolean) =>
    `Откройте эту страницу в Safari на iPhone/iPad${publicMode ? '' : ' в той же локальной сети'} и следуйте инструкциям установки.`,
  shareCopyAlternative:
    'Можно также скопировать ссылку и вставить её в адресную строку Safari на устройстве.',
  shareText: 'Нажмите «Установить» и подтвердите установку в системном окне iOS.',
  copy: 'Скопировать ссылку',
  install: 'Установить',
  installConfirm: 'Подтвердите установку в системном окне iOS.',
  installManifest: 'Загрузка манифеста…',
  installWaitingIpa: 'Манифест передан. Ожидаем начала скачивания.',
  installTransferring: (percent: number) => `Загрузка IPA-файлов (${percent}%)…`,
  installComplete: 'Все файлы переданы. Раздачу можно остановить, если она больше не нужна.',
  installSkipped: (count: number) =>
    `Все запрошенные файлы переданы. Ещё ${count} шт. устройство не запросило — вероятно, они уже установлены. Раздачу можно остановить, если она больше не нужна.`,
  installNothingRequested:
    'Устройство не стало скачивать приложения — вероятно, они уже установлены. Раздачу можно остановить, если она больше не нужна.',
  installHomeScreen: 'Завершение установки проверьте на экране «Домой». Браузер можно свернуть.',
  shareFallback:
    'Если установка не началась, можно попробовать скопировать ссылку и вставить её в адресную строку Safari.',
  copied: 'Скопировано',
  stop: 'Остановить',
  stopShareText: 'Ссылка установки станет недоступна. Приложения останутся в разделе «Загрузки».',
}

export const en: typeof ru = {
  searchShares: 'Search hosting sessions',
  shareOwner: 'Owner',
  shareStarted: 'Hosting started',
  noShares: 'No active hosting sessions',
  viewShare: 'View hosting session',
  confirmStopShare: 'Stop hosting?',
  confirmStopShares: 'Stop selected hosting sessions?',
  moreSharedFiles: (count: number) => `and ${count} more`,
  shareTitle: 'Your apps are ready to install',
  shareSafariRequired: (publicMode: boolean) =>
    `Open this page in Safari on your iPhone/iPad${publicMode ? '' : ' on the same local network'} and follow the installation instructions.`,
  shareCopyAlternative:
    'Alternatively, copy the link and paste it into the Safari address bar on your device.',
  shareText: 'Tap Install, then confirm the installation in the iOS system prompt.',
  copy: 'Copy link',
  install: 'Install',
  installConfirm: 'Confirm the installation in the iOS system prompt.',
  installManifest: 'Downloading the manifest…',
  installWaitingIpa: 'Manifest sent. Waiting for the download to start.',
  installTransferring: (percent: number) => `Downloading IPA files (${percent}%)…`,
  installComplete: 'All files have been sent. You can stop hosting if you no longer need it.',
  installSkipped: (count: number) =>
    `All requested files have been sent. The device did not request ${count} more ${count === 1 ? 'app' : 'apps'}, probably because ${count === 1 ? 'it is' : 'they are'} already installed. You can stop hosting if you no longer need it.`,
  installNothingRequested:
    'The device did not download the apps, probably because they are already installed. You can stop hosting if you no longer need it.',
  installHomeScreen:
    'Check that installation has finished on your Home Screen. You can leave the browser in the background.',
  shareFallback:
    'If installation does not start, you can try copying the link and pasting it into the Safari address bar.',
  copied: 'Copied',
  stop: 'Stop',
  stopShareText: 'The install link will stop working. Your apps will stay in Downloads.',
}
