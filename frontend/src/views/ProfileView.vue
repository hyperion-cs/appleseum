<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { apiErrorCode } from '../api/errors.ts'
import { api } from '../api/http.ts'
import type { State, ProfileSummary } from '../api/types.ts'
import { useAlive, useLatest } from '../composables/lifetime.ts'
import { useAction } from '../composables/useAction.ts'
import { date, region } from '../i18n/format.ts'
import { t, useErrorMessage } from '../i18n/index.ts'
import ConfirmationContent from '../components/ConfirmationContent.vue'
import Icon from '../components/Icon.vue'
import MenuButton from '../components/MenuButton.vue'
import MessageDialog from '../components/MessageDialog.vue'
import Modal from '../components/Modal.vue'

const props = defineProps<{ state: State; loggingOut: boolean }>()
const emit = defineEmits<{ logout: [] }>()
const alive = useAlive()
const latest = useLatest()
const profile = ref<ProfileSummary | null>(null)
const loading = ref(false)
const dialog = ref<'menu' | 'clearCache' | null>(null)
const { code: errorCode } = useErrorMessage()
const { busy, run } = useAction(errorCode)
const fields = computed(() => {
  const { account } = props.state
  const signedInAt = account?.signedInAt
  const apple = profile.value?.apple
  const roles: Record<string, string> = {
    organizer: t.value.profileFamilyOrganizer,
    member: t.value.profileFamilyMember,
    none: t.value.profileFamilyNone,
  }
  return [
    { label: t.value.profileName, value: account?.name },
    { label: t.value.email, value: account?.email },
    { label: t.value.accountId, value: account?.id },
    { label: t.value.country, value: account?.country && region(account.country) },
    { label: t.value.profileNickname, value: apple?.nickname },
    { label: t.value.profileFamily, value: roles[apple?.familyRole ?? ''] },
    { label: t.value.profileBalance, value: apple?.balance },
    { label: t.value.profileSubscriptions, value: apple?.subscriptions?.toString() },
    {
      label: t.value.profileCatalog,
      value:
        profile.value &&
        t.value.profileCatalogValue(profile.value.catalogApps, profile.value.delistedApps),
    },
    { label: t.value.profileSignedInAt, value: signedInAt && date(signedInAt) },
  ].filter((field) => field.value)
})

async function load(refresh = false) {
  if (loading.value || busy.value) return
  const current = latest.start()
  loading.value = true
  try {
    const value = await (refresh
      ? api.post<ProfileSummary>('/profile/refresh')
      : api.get<ProfileSummary>('/profile'))
    if (!current()) return
    profile.value = value
    errorCode.value = value.appleError
  } catch (e) {
    if (current()) errorCode.value = apiErrorCode(e)
  } finally {
    if (current()) loading.value = false
  }
}

function clearCache() {
  if (loading.value) return
  return run(async () => {
    await api.delete('/profile')
    const value = await api.get<ProfileSummary>('/profile')
    if (!alive()) return
    profile.value = value
    dialog.value = null
  })
}

function closeDialog() {
  if (busy.value) return
  dialog.value = null
  errorCode.value = ''
}

onMounted(() => load())
</script>

<template>
  <section class="profile-section" :aria-label="t.profile">
    <div class="panel">
      <p v-if="loading && !profile" class="muted" role="status">{{ t.profileLoading }}</p>
      <p v-if="profile && !profile.apple && !loading" class="muted">{{ t.profileEmpty }}</p>
      <dl class="profile-details">
        <div v-for="field in fields" :key="field.label" class="profile-field">
          <dt>{{ field.label }}</dt>
          <dd>{{ field.value }}</dd>
        </div>
      </dl>
      <div class="profile-actions">
        <button
          :class="{ pending: loading }"
          :disabled="loading || busy"
          :aria-busy="loading"
          @click="load(true)"
        >
          <Icon name="refresh" :size="18" />
          {{ t.refresh }}
        </button>
        <button class="danger" :disabled="loggingOut || busy" @click="emit('logout')">
          <Icon name="logout" :size="18" />
          {{ t.logout }}
        </button>
        <button
          class="icon-button"
          :disabled="loading || busy"
          :aria-label="t.more"
          :title="t.more"
          @click="dialog = 'menu'"
        >
          <Icon name="more" />
        </button>
      </div>
    </div>
  </section>

  <MessageDialog v-if="errorCode && !dialog" :code="errorCode" @close="errorCode = ''" />
  <Modal
    v-if="dialog"
    :dismissible="!busy"
    :title="dialog === 'menu' ? t.more : errorCode ? t.actionFailed : t.clearProfileCache"
    @close="closeDialog"
  >
    <div v-if="dialog === 'menu'" class="menu-actions">
      <MenuButton
        icon="trash"
        :disabled="busy || loading || (!profile?.apple && !profile?.appleError)"
        @click="((errorCode = ''), (dialog = 'clearCache'))"
      >
        {{ t.clearProfileCache }}
      </MenuButton>
    </div>
    <ConfirmationContent
      v-else
      :action="t.clearData"
      :busy="busy"
      :error="errorCode"
      @close="closeDialog"
      @confirm="clearCache"
    >
      <p>{{ t.clearProfileCacheConfirm }}</p>
    </ConfirmationContent>
  </Modal>
</template>

<style>
.profile-section .panel {
  padding: var(--s4);
}
.profile-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s2);
  justify-content: flex-end;
  padding-top: var(--s3);
  border-top: 1px solid var(--subtle-line);
}
@media (max-width: 600px) {
  .profile-actions {
    justify-content: flex-start;
  }
}
</style>
