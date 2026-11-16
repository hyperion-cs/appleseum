import { watch } from 'vue'
import type { Router, RouteRecordName } from 'vue-router'

type NavigationAccess = {
  signedIn: boolean
  debug: boolean
  admin: boolean
  service: boolean
  public: boolean
}

export function installAuthNavigation(router: Router, access: () => NavigationAccess | null) {
  let destination: 'catalog' | 'library' | 'profile' = 'catalog'

  function redirect(name: RouteRecordName | null | undefined) {
    const user = access()
    if (!user) return
    const { signedIn, service, debug, admin } = user
    const home = service || (!signedIn && !user.public) ? 'library' : 'catalog'
    if (signedIn && service) return name === 'library' ? undefined : { name: 'library' }
    if (name === 'debug' || name === 'settings')
      return (name === 'debug' ? debug : admin)
        ? undefined
        : { name: signedIn || !user.public ? home : 'login' }
    if (!signedIn && name !== 'login' && (user.public || name !== 'library')) {
      if (name === 'catalog' || name === 'library' || name === 'profile') destination = name
      return { name: user.public ? 'login' : 'library' }
    }
    if (signedIn && name === 'login') return { name: destination }
  }

  const removeGuard = router.beforeEach((to) => redirect(to.name))
  const stopWatching = watch(
    access,
    () => {
      const target = redirect(router.currentRoute.value.name)
      if (target) void router.replace(target)
    },
    { immediate: true, flush: 'sync' },
  )
  return () => {
    removeGuard()
    stopWatching()
  }
}
