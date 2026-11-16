<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useTimeoutFn } from '@vueuse/core'
import type { InstallationStage, Share } from '../api/types.ts'
import { useAlive } from '../composables/lifetime.ts'
import { useInstallation } from '../composables/useInstallation.ts'
import { errorText, t } from '../i18n/index.ts'
import { copyText } from '../platform/browser.ts'
import { iosSafari } from '../platform/device.ts'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import MessageContent from '../components/MessageContent.vue'

const copiedNoticeMs = 5_000

const props = defineProps<{ share: Share; publicMode: boolean; error?: string }>()
const emit = defineEmits<{ close: [] }>()
const alive = useAlive()
const copying = ref(false)
const failure = ref('')
const installation = reactive(useInstallation(() => props.share.id))
// iOS does not request apps it already has; the server reports them as skipped.
function completed(skipped: number) {
  if (skipped === 0) return t.value.installComplete
  if (skipped >= props.share.count) return t.value.installNothingRequested
  return t.value.installSkipped(skipped)
}

const installationMessage = computed(() => {
  const problem = installation.error || installation.warning
  if (problem) return errorText(problem)
  const transferring = t.value.installTransferring(installation.progress?.percent ?? 0)
  const messages: Record<InstallationStage, string> = {
    waiting: t.value.installConfirm,
    manifest: t.value.installManifest,
    waitingIpa: t.value.installWaitingIpa,
    transferring,
    interrupted: transferring,
    failed: '',
    complete: completed(installation.progress?.skipped ?? 0),
  }
  return messages[installation.stage]
})
const installationTone = computed(() => {
  if (installation.error === 'installation_manifest_failed') return 'danger'
  if (installation.error || installation.warning) return 'warning'
  if (installation.stage === 'complete')
    return installation.progress?.skipped ? 'warning' : 'success'
  return 'info'
})
function fail(code: string) {
  if (!code || failure.value) return
  failure.value = code
  installation.stop()
}

watch(
  () => props.error,
  (code) => {
    if (code) fail(code)
  },
  { immediate: true },
)

const canInstall = iosSafari(navigator.userAgent, navigator.maxTouchPoints)
const { isPending: copied, start: showCopied } = useTimeoutFn(() => {}, copiedNoticeMs, {
  immediate: false,
})

function install(event: MouseEvent) {
  // Set the native link synchronously: iOS must receive the handoff during
  // the user's activation, without waiting for Vue's DOM update or the API.
  const link = event.currentTarget as HTMLAnchorElement
  link.href = installation.start(props.share.url)
}

async function copy() {
  if (copying.value) return
  copying.value = true
  try {
    await copyText(props.share.url)
    if (alive()) showCopied()
  } catch {
    if (alive()) fail('copy_failed')
  } finally {
    copying.value = false
  }
}
</script>
<template>
  <Modal :title="failure ? t.actionFailed : t.shareTitle" @close="emit('close')">
    <MessageContent v-if="failure" :code="failure" @close="emit('close')" />
    <template v-else>
      <template v-if="canInstall">
        <div v-if="installation.requested" class="install-feedback">
          <p
            class="installation-status"
            :class="`installation-status-${installationTone}`"
            :role="installation.error || installation.warning ? 'alert' : 'status'"
            aria-atomic="true"
          >
            {{ installationMessage }}
          </p>
          <p class="muted">{{ t.installHomeScreen }}</p>
        </div>
        <p v-else>{{ t.shareText }}</p>
        <p>{{ t.shareFallback }}</p>
      </template>
      <template v-else>
        <p>{{ t.shareSafariRequired(publicMode) }}</p>
        <p>{{ t.shareCopyAlternative }}</p>
      </template>
      <div class="share-actions">
        <a
          v-if="canInstall"
          class="button wide"
          :href="share.url"
          target="_blank"
          rel="noopener noreferrer"
          @click="install"
        >
          <Icon name="download" :size="18" />
          {{ t.install }}
        </a>
        <button
          class="wide"
          :disabled="copying"
          :aria-busy="copying"
          :class="{ pending: copying }"
          @click="copy"
        >
          <Icon :name="copied ? 'check' : 'copy'" :size="18" />
          <span aria-live="polite">{{ copied ? t.copied : t.copy }}</span>
        </button>
      </div>
    </template>
  </Modal>
</template>

<style scoped>
.installation-status {
  border-radius: var(--r2);
  padding: var(--s3) var(--s4);
  margin-bottom: var(--s4);
  overflow-wrap: anywhere;
}
.installation-status-info {
  color: var(--accent);
  background: var(--selected);
}
.installation-status-success {
  color: var(--success);
  background: var(--success-bg);
}
.installation-status-warning {
  color: var(--warning);
  background: var(--warning-bg);
}
.installation-status-danger {
  color: var(--danger);
  background: var(--danger-bg);
}
</style>
