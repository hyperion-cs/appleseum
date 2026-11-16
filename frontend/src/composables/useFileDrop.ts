import { computed, ref } from 'vue'

// One drag session for every element bound to `handlers`, such as a page
// and a dialog teleported above it.
export function useFileDrop(options: {
  enabled: () => boolean
  ready: () => boolean
  drop: (files: File[]) => void
}) {
  const depth = ref(0)

  function handle(event: DragEvent) {
    if (!options.enabled() || !event.dataTransfer?.types.includes('Files')) return
    event.preventDefault()
    switch (event.type) {
      case 'dragenter':
        if (options.ready()) depth.value++
        break
      case 'dragleave':
        depth.value = Math.max(0, depth.value - 1)
        break
      case 'dragover':
        event.dataTransfer.dropEffect = options.ready() ? 'copy' : 'none'
        break
      case 'drop':
        depth.value = 0
        options.drop([...event.dataTransfer.files])
    }
  }

  return {
    dragging: computed(() => depth.value > 0),

    reset() {
      depth.value = 0
    },

    handlers: { dragenter: handle, dragover: handle, dragleave: handle, drop: handle },
  }
}
