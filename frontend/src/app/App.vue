<script setup lang="ts">
import { computed, defineAsyncComponent, onScopeDispose, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { productDisplayName, productVersion, repositoryUrl } from '../api/types.ts'
import { useSession } from '../composables/useSession.ts'
import { theme } from '../composables/usePreferences.ts'
import { language, languages, nextLanguage, t } from '../i18n/index.ts'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Icon from '../components/Icon.vue'
import MessageDialog from '../components/MessageDialog.vue'
import CatalogView from '../views/CatalogView.vue'
import LibraryView from '../views/LibraryView.vue'
import LoginView from '../views/LoginView.vue'
import { loadSettings, loadDebug, loadProfile, warm } from '../views/lazy.ts'
import { installAuthNavigation } from './authNavigation.ts'
import { useHeaderLayout } from './useHeaderLayout.ts'

const version = import.meta.env.VITE_APP_VERSION || productVersion
// The release build bakes in its commit; "-x" marks uncommitted changes.
const commit: string = import.meta.env.VITE_APP_COMMIT || ''
const commitUrl = `${repositoryUrl}/commit/${commit.replace(/-x$/, '')}`
const SettingsView = defineAsyncComponent(loadSettings)
const DebugView = defineAsyncComponent(loadDebug)
const ProfileView = defineAsyncComponent(loadProfile)
const route = useRoute()
const router = useRouter()
const session = useSession()
const systemNotice = computed(() => !!session.error || session.connectionError)
const libraryAvailable = computed(
  () => session.current !== null && (!session.current.mode.public || session.authenticated),
)
const hasSections = computed(
  () => libraryAvailable.value || session.current?.mode.debug || session.current?.mode.admin,
)
const sections = computed(() => {
  const personal = session.authenticated && !session.current?.mode.service
  return [
    { name: 'login', icon: 'lock', label: t.value.signIn, shown: !session.authenticated },
    { name: 'catalog', icon: 'app', label: t.value.available, shown: personal },
    { name: 'library', icon: 'download', label: t.value.downloads, shown: libraryAvailable.value },
    {
      name: 'profile',
      icon: 'profile',
      label: t.value.profile,
      shown: personal,
      load: loadProfile,
    },
    {
      name: 'debug',
      icon: 'debug',
      label: t.value.debug,
      shown: session.current?.mode.debug,
      load: loadDebug,
    },
    {
      name: 'settings',
      icon: 'settings',
      label: t.value.settings,
      shown: session.current?.mode.admin,
      load: loadSettings,
    },
  ].filter((section) => section.shown)
})
const header = ref<HTMLElement>()
const sectionsInline = useHeaderLayout(header, [sections, hasSections, () => route.name])
const workspace = computed(() =>
  session.current?.mode.service
    ? 'library'
    : route.name === 'library' || route.name === 'profile'
      ? route.name
      : 'catalog',
)
const librarySection = ref<'downloads' | 'shares'>('downloads')
const downloadsStarted = ref(0)

function showDownloads() {
  librarySection.value = 'downloads'
  downloadsStarted.value++
}

watch(
  () => session.authenticated && session.current?.shareCount,
  (shares) => {
    if (!shares) librarySection.value = 'downloads'
  },
)

const confirmingLogout = ref(false)

function requestLogout() {
  session.actionError = ''
  confirmingLogout.value = !session.busy && session.signedIn
}

async function logout() {
  if (await session.logout()) confirmingLogout.value = false
}

function closeLogout() {
  confirmingLogout.value = false
  session.actionError = ''
}

watch(
  () => session.signedIn,
  (signedIn) => {
    if (!signedIn) confirmingLogout.value = false
  },
)
onScopeDispose(
  installAuthNavigation(
    router,
    () =>
      session.current && {
        signedIn: session.authenticated,
        debug: session.current.mode.debug,
        admin: session.current.mode.admin,
        service: session.current.mode.service,
        public: session.current.mode.public,
      },
  ),
)
</script>

<template>
  <div class="shell">
    <header
      ref="header"
      class="topbar"
      :class="{
        'sections-stacked': hasSections && !sectionsInline,
        'sections-inline': hasSections && sectionsInline,
      }"
    >
      <RouterLink
        class="brand"
        :to="{ name: !session.authenticated && libraryAvailable ? 'library' : 'catalog' }"
        :aria-label="productDisplayName"
      >
        <img class="brand-mark" src="/logo.svg" alt="" width="34" height="34" />
        {{ productDisplayName }}
      </RouterLink>
      <nav v-if="hasSections" class="tabs" data-scrollbar :aria-label="t.sections">
        <RouterLink
          v-for="section in sections"
          :key="section.name"
          :class="{ chosen: route.name === section.name }"
          :to="{ name: section.name }"
          @pointerenter="section.load && warm(section.load)"
          @focus="section.load && warm(section.load)"
        >
          <Icon :name="section.icon" :size="18" />
          <span>{{ section.label }}</span>
        </RouterLink>
      </nav>
      <div class="preferences">
        <button
          class="text-button"
          :aria-label="languages[nextLanguage].name"
          @click="session.toggleLanguage"
        >
          {{ language.toUpperCase() }}
        </button>
        <button
          class="icon-button"
          :aria-label="theme === 'dark' ? t.light : t.dark"
          @click="session.toggleTheme"
        >
          <Icon :name="theme === 'dark' ? 'sun' : 'moon'" />
        </button>
        <button
          v-if="session.signedIn"
          class="icon-button danger"
          :aria-label="t.logout"
          :title="t.logout"
          :disabled="session.busy"
          @click="requestLogout"
        >
          <Icon name="logout" />
        </button>
      </div>
    </header>
    <div v-if="systemNotice" class="notice" role="alert">
      {{
        session.connectionError
          ? session.current?.mode.ish
            ? t.ishConnectionLost
            : t.connectionLost
          : session.error
      }}
    </div>
    <div v-if="!session.current && !systemNotice" class="welcome">
      <div class="skeleton hero-skeleton"></div>
      <div class="panel skeleton form-skeleton"></div>
    </div>
    <Suspense v-else-if="session.current">
      <SettingsView
        v-if="session.current.mode.admin && route.name === 'settings'"
        :state="{
          ...session.current,
          auth: {
            ...session.current.auth,
            busy:
              session.current.auth.busy ||
              session.busy ||
              (session.twoFactor && Boolean(session.current.account)),
          },
        }"
        @changed="session.deviceChanged"
      />
      <DebugView
        v-else-if="session.current.mode.debug && route.name === 'debug'"
        :state="session.current"
        :system-notice="systemNotice"
      />
      <LoginView
        v-else-if="!session.authenticated && !(libraryAvailable && route.name === 'library')"
        :session="session"
      />
      <!-- Lists keep their search, page and transfers while another tab is open. -->
      <KeepAlive
        v-else-if="route.name !== 'login' && route.name !== 'debug'"
        :key="session.current.account?.id ?? (session.current.mode.service ? 'service' : 'local')"
        exclude="ProfileView"
      >
        <ProfileView
          v-if="workspace === 'profile'"
          :state="session.current"
          :logging-out="session.busy"
          @logout="requestLogout"
        />
        <LibraryView
          v-else-if="workspace === 'library'"
          v-model:section="librarySection"
          :state="session.current"
          :system-notice="systemNotice"
          :downloads-started="downloadsStarted"
          @expired="session.refresh"
          @state="session.acceptState"
        />
        <CatalogView
          v-else
          :state="session.current"
          :system-notice="systemNotice"
          @expired="session.refresh"
          @state="session.acceptState"
          @downloads="showDownloads"
        />
      </KeepAlive>
      <template #fallback>
        <main class="workspace" aria-busy="true">
          <div class="panel skeleton form-skeleton"></div>
        </main>
      </template>
    </Suspense>
    <footer class="app-footer">
      <span>
        {{ productDisplayName }} v{{ version }}
        <span v-if="commit" class="commit">
          <a :href="commitUrl" target="_blank" rel="noopener noreferrer">{{ commit }}</a>
        </span>
      </span>
      <span aria-hidden="true">·</span>
      <a :href="repositoryUrl" target="_blank" rel="noopener noreferrer">GitHub</a>
    </footer>
    <MessageDialog
      v-if="session.actionError && !confirmingLogout"
      :code="session.actionError"
      @close="session.actionError = ''"
    />
    <ConfirmDialog
      v-if="confirmingLogout"
      :title="t.confirmLogout"
      :action="t.logout"
      :busy="session.busy"
      :error="session.actionError"
      @close="closeLogout"
      @confirm="logout"
    >
      <p>{{ t.logoutText }}</p>
    </ConfirmDialog>
  </div>
</template>

<style>
.shell {
  max-width: var(--max);
  margin: auto;
  padding: 0 var(--s8);
}
.topbar {
  --header-height: var(--s16);
  --header-gap: var(--s4);
  position: relative;
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr) max-content;
  grid-template-rows: var(--header-height);
  column-gap: 0;
  align-items: center;
}
.topbar.sections-inline {
  column-gap: var(--header-gap);
}
.topbar > .brand {
  grid-column: 1;
  grid-row: 1;
}
.topbar > .preferences {
  grid-column: 3;
  grid-row: 1;
}
.topbar > .tabs {
  grid-column: 2;
  grid-row: 1;
  justify-self: center;
  margin: 0;
}
.topbar.sections-stacked {
  grid-template-rows: var(--header-height) auto;
}
.topbar.sections-stacked > .tabs {
  grid-column: 1 / -1;
  grid-row: 2;
  width: 100%;
  margin-top: var(--s3);
}
.sections-stacked .tabs a {
  flex: 1;
}
.topbar .tabs.measuring-tabs {
  width: max-content;
  max-width: none;
}
.topbar .tabs.measuring-tabs a {
  flex: none;
}
.brand {
  text-decoration: none;
  font-size: var(--heading);
  font-weight: var(--bold);
  letter-spacing: -0.8px;
  display: flex;
  align-items: center;
  gap: var(--s3);
}
.brand-mark {
  height: 34px;
  width: 34px;
  display: block;
  flex-shrink: 0;
}
.preferences {
  display: flex;
  gap: var(--s1);
}
.tabs {
  display: flex;
  background: var(--navigation-bg);
  padding: var(--s1);
  border-radius: var(--r2);
  width: max-content;
  max-width: 100%;
  gap: var(--s1);
}
.tabs a {
  text-decoration: none;
  border: 0;
  background: transparent;
  min-height: var(--control);
  font-size: var(--tab-font-size);
  color: var(--muted);
}
.tabs a.chosen {
  background: var(--primary);
  color: var(--on-primary);
  box-shadow: none;
}
@media (max-width: 600px) {
  .topbar .brand {
    gap: var(--s2);
  }
}
</style>
