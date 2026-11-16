export const routePaths = {
  login: '/login',
  catalog: '/available',
  library: '/downloads',
  profile: '/profile',
  debug: '/debug',
  settings: '/settings',
} as const

export const routeRedirects = {
  '/downloaded': routePaths.library,
  '/appstore': routePaths.catalog,
  '/localLibrary': routePaths.library,
} as const
