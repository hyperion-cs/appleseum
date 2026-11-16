import { test } from 'node:test'
import assert from 'node:assert/strict'
import { ref } from 'vue'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import { installAuthNavigation } from '../src/app/authNavigation.ts'
import { routePaths } from '../src/app/routes.ts'

async function setup(
  path: string,
  debugging = false,
  admin = false,
  service = false,
  publicMode = true,
) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: Object.entries(routePaths).map(([name, path]) => ({
      name,
      path,
      component: { render: () => null },
    })),
  })
  await router.push(path)
  const authenticated = ref<boolean | null>(null)
  const stop = installAuthNavigation(router, () =>
    authenticated.value === null
      ? null
      : {
          signedIn: authenticated.value,
          debug: debugging,
          admin,
          service,
          public: publicMode,
        },
  )
  return { router, authenticated, stop }
}

function nextNavigation(router: Router) {
  return new Promise<unknown>((resolve, reject) => {
    const timer = setTimeout(() => {
      remove()
      reject(new Error('Navigation timed out'))
    }, 1_000)
    const remove = router.afterEach((to, _from, failure) => {
      clearTimeout(timer)
      remove()
      if (failure) reject(failure)
      else resolve(to.name)
    })
  })
}

test('a guest goes to login and returns to the requested section after signing in', async () => {
  const { router, authenticated, stop } = await setup('/downloads')
  try {
    assert.equal(router.currentRoute.value.path, '/downloads')
    let arrived = nextNavigation(router)
    authenticated.value = false
    assert.equal(await arrived, 'login')
    arrived = nextNavigation(router)
    authenticated.value = true
    assert.equal(await arrived, 'library')
    await router.push('/profile')
    arrived = nextNavigation(router)
    authenticated.value = false
    assert.equal(await arrived, 'login')
    arrived = nextNavigation(router)
    authenticated.value = true
    assert.equal(await arrived, 'profile')
  } finally {
    stop()
  }
})

test('the service account opens downloads and cannot open Apple or device sections', async () => {
  const { router, authenticated, stop } = await setup('/login', false, false, true)
  try {
    const arrived = nextNavigation(router)
    authenticated.value = true
    assert.equal(await arrived, 'library')
    for (const path of [routePaths.catalog, routePaths.profile, routePaths.settings]) {
      await router.push(path)
      await new Promise((resolve) => setTimeout(resolve, 0))
      assert.equal(router.currentRoute.value.name, 'library')
    }
  } finally {
    stop()
  }
})

test('a signed-in visit to login opens Available; an authenticated deep link stays intact', async () => {
  const { router, authenticated, stop } = await setup('/login')
  try {
    const arrived = nextNavigation(router)
    authenticated.value = true
    assert.equal(await arrived, 'catalog')
    await router.push('/downloads')
    assert.equal(router.currentRoute.value.name, 'library')
  } finally {
    stop()
  }
})

test('debug is accessible before login only when the server enables it', async () => {
  for (const enabled of [false, true]) {
    const { router, authenticated, stop } = await setup('/debug', enabled)
    try {
      const arrived = enabled ? null : nextNavigation(router)
      authenticated.value = false
      if (arrived) assert.equal(await arrived, 'login')
      else assert.equal(router.currentRoute.value.name, 'debug')
      if (enabled) {
        authenticated.value = true
        assert.equal(router.currentRoute.value.name, 'debug')
      }
    } finally {
      stop()
    }
  }
})

test('settings requires admin independently of authentication and debug', async () => {
  for (const admin of [false, true]) {
    const { router, authenticated, stop } = await setup('/settings', !admin, admin)
    try {
      const arrived = admin ? null : nextNavigation(router)
      authenticated.value = false
      if (arrived) assert.equal(await arrived, 'login')
      else assert.equal(router.currentRoute.value.name, 'settings')
      authenticated.value = true
      await router.isReady()
      await router.push('/settings')
      await new Promise((resolve) => setTimeout(resolve, 0))
      assert.equal(router.currentRoute.value.name, admin ? 'settings' : 'catalog')
    } finally {
      stop()
    }
  }
})

test('local guests can open the library and login; Apple sections lead to the library', async () => {
  for (const path of ['/available', '/downloads', '/profile', '/login']) {
    const { router, authenticated, stop } = await setup(path, false, false, false, false)
    try {
      authenticated.value = false
      await new Promise((resolve) => setTimeout(resolve, 0))
      assert.equal(router.currentRoute.value.name, path === '/login' ? 'login' : 'library')
      await router.push('/login')
      assert.equal(router.currentRoute.value.name, 'login')
      await router.push('/downloads')
      assert.equal(router.currentRoute.value.name, 'library')
    } finally {
      stop()
    }
  }
})

test('local sign-out returns to the library and sign-in unlocks Apple sections', async () => {
  const { router, authenticated, stop } = await setup('/login', false, false, false, false)
  try {
    authenticated.value = false
    let arrived = nextNavigation(router)
    authenticated.value = true
    assert.equal(await arrived, 'catalog')
    await router.push('/profile')
    arrived = nextNavigation(router)
    authenticated.value = false
    assert.equal(await arrived, 'library')
    await router.push('/login')
    arrived = nextNavigation(router)
    authenticated.value = true
    assert.equal(await arrived, 'profile')
    await router.push('/downloads')
    authenticated.value = false
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.equal(router.currentRoute.value.name, 'library')
  } finally {
    stop()
  }
})

test('the router rejects a forbidden destination before committing it', async () => {
  const { router, authenticated, stop } = await setup('/login')
  const visited: unknown[] = []
  const remove = router.afterEach((to, _from, failure) => {
    if (!failure) visited.push(to.name)
  })
  try {
    authenticated.value = false
    await router.push('/settings')
    await router.push('/debug')
    assert.equal(router.currentRoute.value.name, 'login')
    assert.deepEqual(visited, [])
    const arrived = nextNavigation(router)
    authenticated.value = true
    assert.equal(await arrived, 'catalog')
    await router.push('/profile')
    assert.deepEqual(visited, ['catalog', 'profile'])
  } finally {
    remove()
    stop()
  }
})
