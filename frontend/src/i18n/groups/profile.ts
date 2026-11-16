// Account profile.
export const ru = {
  profileCatalog: 'Приложений в каталоге',
  profileCatalogValue: (total: number, delisted: number) =>
    `${total}, из них ${delisted} скрыты в App Store`,
  profileNickname: 'Псевдоним',
  profileFamily: 'Семейный доступ',
  profileFamilyOrganizer: 'Организатор',
  profileFamilyMember: 'Участник',
  profileFamilyNone: 'Не используется',
  profileBalance: 'Баланс Apple',
  profileSubscriptions: 'Подписок',
  profileLoading: 'Загружаем данные профиля…',
  profileEmpty: 'Нажмите «Обновить», чтобы получить сведения профиля из Apple.',
  profileSignedInAt: 'Вход выполнен',
  email: 'Apple ID',
  profileName: 'Имя',
  accountId: 'ID аккаунта',
  country: 'Страна App Store',
  clearProfileCacheConfirm:
    'Сохранённые сведения профиля будут удалены. Чтобы получить их заново, нажмите «Обновить». Вход и скачанные IPA сохранятся.',
  clearProfileCache: 'Очистить кэш профиля',
}

export const en: typeof ru = {
  profileCatalog: 'Apps in the catalog',
  profileCatalogValue: (total: number, delisted: number) =>
    `${total}, of which ${delisted} are delisted`,
  profileNickname: 'Nickname',
  profileFamily: 'Family Sharing',
  profileFamilyOrganizer: 'Organizer',
  profileFamilyMember: 'Member',
  profileFamilyNone: 'Not in use',
  profileBalance: 'Apple balance',
  profileSubscriptions: 'Subscriptions',
  profileLoading: 'Loading profile…',
  profileEmpty: 'Click Refresh to get your profile details from Apple.',
  profileSignedInAt: 'Signed in at',
  email: 'Apple ID',
  profileName: 'Name',
  accountId: 'Account ID',
  country: 'App Store country',
  clearProfileCacheConfirm:
    'Saved profile details will be deleted. Click Refresh to fetch them again. Your sign-in and downloaded IPAs will be kept.',
  clearProfileCache: 'Clear profile cache',
}
