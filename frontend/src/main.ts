import { createApp, effectScope, onScopeDispose } from 'vue'
import { RouterView } from 'vue-router'
import { router } from './app/router.ts'
import './style.css'
import { preventPinchZoom } from './app/gestures.ts'
import { installPersistentScrollbars } from './app/persistentScrollbars.ts'

const app = createApp(RouterView).use(router)
const browser = effectScope()
browser.run(() => {
  preventPinchZoom()
  onScopeDispose(installPersistentScrollbars())
})
app.onUnmount(() => browser.stop())
void router.isReady().then(() => app.mount('#app'))
