import { createRouter, createWebHistory, type RouteLocation } from 'vue-router'
import { demoEnabled } from '../platform/browser.ts'
import App from './App.vue'
import { routePaths, routeRedirects } from './routes.ts'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    ...Object.entries(routePaths).map(([name, path]) => ({ name, path, component: App })),
    ...Object.entries(routeRedirects).map(([path, target]) => ({
      path,

      redirect: ({ query, hash }: RouteLocation) => ({ path: target, query, hash }),
    })),
    { path: '/:pathMatch(.*)*', redirect: routePaths.login },
  ],
})

router.beforeEach((to) => {
  // The demo flag is read from the address on load, so navigation must keep it.
  if (demoEnabled && !('demo' in to.query))
    return { path: to.path, query: { ...to.query, demo: null }, hash: to.hash, replace: true }
})
