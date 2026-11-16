import { useEventListener } from '@vueuse/core'

export function preventPinchZoom() {
  useEventListener(document, ['gesturestart', 'gesturechange'], (event) => event.preventDefault(), {
    passive: false,
  })
}
