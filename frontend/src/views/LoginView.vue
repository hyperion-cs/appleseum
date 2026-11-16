<script setup lang="ts">
import { ref, watch } from 'vue'
import type { Session } from '../composables/useSession.ts'
import { t } from '../i18n/index.ts'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'

const props = defineProps<{ session: Session }>()
const passwordVisible = ref(false)
const showingCodeHelp = ref(false)

function login() {
  if (props.session.canSubmit) passwordVisible.value = false
  return props.session.login()
}

function enter(event: KeyboardEvent) {
  if (
    event.isComposing ||
    !(event.target instanceof HTMLInputElement) ||
    event.target.type === 'checkbox'
  )
    return
  event.preventDefault()
  if (matchMedia('(hover: hover) and (pointer: fine)').matches) void login()
}

watch(
  () => props.session.signedIn,
  (signedIn) => {
    if (!signedIn) showingCodeHelp.value = false
  },
)
</script>

<template>
  <main class="welcome">
    <div class="intro">
      <h1>{{ t.title }}</h1>
      <p>{{ t.intro }}</p>
    </div>
    <section class="panel login-panel">
      <div class="login-heading">
        <span class="section-icon"><Icon name="lock" :size="25" /></span>
        <h2>{{ t.signInTitle }}</h2>
      </div>
      <p>{{ t.signInText }}</p>
      <div v-if="session.busy" class="login-progress" role="status" aria-live="polite">
        <span class="spinner"></span>
        <span>{{ t.signingIn }}</span>
      </div>
      <form v-else @submit.prevent="" @keydown.enter="enter">
        <template v-if="!session.savedVerification">
          <label>
            {{ t.loginIdentifier }}
            <span class="required">*</span>
            <input
              v-model="session.form.email"
              type="text"
              autocomplete="username"
              autocapitalize="none"
              spellcheck="false"
              maxlength="320"
            />
          </label>
          <label for="login-password">
            {{ t.password }}
            <span class="required">*</span>
            <span class="password-field">
              <button
                class="icon-button text-button password-toggle"
                type="button"
                :aria-label="passwordVisible ? t.hidePassword : t.showPassword"
                :title="passwordVisible ? t.hidePassword : t.showPassword"
                :aria-pressed="passwordVisible"
                aria-controls="login-password"
                @keydown.enter.stop
                @mousedown.prevent
                @click="passwordVisible = !passwordVisible"
              >
                <Icon :name="passwordVisible ? 'eyeOff' : 'eye'" />
              </button>
              <input
                id="login-password"
                v-model="session.form.password"
                :type="passwordVisible ? 'text' : 'password'"
                autocomplete="current-password"
                autocapitalize="none"
                spellcheck="false"
                maxlength="1024"
              />
            </span>
          </label>
        </template>
        <label v-if="session.twoFactor">
          {{ t.code }}
          <span class="required">*</span>
          <input
            v-model="session.form.code"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="6"
            @input="session.form.code = session.form.code.replace(/\D/g, '')"
          />
          <small>{{ t.twoFactor }}</small>
        </label>
        <label v-if="!session.savedVerification" class="remember-me">
          <input v-model="session.form.remember" type="checkbox" />
          {{ t.rememberMe }}
        </label>
        <button class="primary wide" type="button" :disabled="!session.canSubmit" @click="login">
          {{ t.signIn }}
        </button>
        <div v-if="session.twoFactor" class="login-actions">
          <button class="text-button" type="button" @click="showingCodeHelp = true">
            {{ t.codeHelp }}
          </button>
          <button class="text-button" type="button" @click="session.restartLogin">
            {{ t.retryLogin }}
          </button>
        </div>
      </form>
    </section>
  </main>
  <Modal v-if="showingCodeHelp" :title="t.codeHelp" @close="showingCodeHelp = false">
    <div class="code-help">
      <section>
        <h3>{{ t.codeHelpCredentialsTitle }}</h3>
        <p>{{ t.codeHelpCredentials }}</p>
      </section>
      <section>
        <h3>{{ t.codeHelpOfflineTitle }}</h3>
        <p>{{ t.codeHelpOfflineDevice }}</p>
        <ol>
          <li>{{ t.codeHelpOfflineDisconnect }}</li>
          <li>{{ t.codeHelpOfflineSettings }}</li>
          <li>{{ t.codeHelpOfflineGenerate }}</li>
          <li>{{ t.codeHelpOfflineReturn }}</li>
        </ol>
      </section>
    </div>
  </Modal>
</template>

<style>
.login-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--s1) var(--s2);
  margin-top: var(--s1);
}
.login-heading {
  display: flex;
  align-items: center;
  gap: var(--s3);
}
.login-heading .section-icon {
  flex-shrink: 0;
  margin: 0;
}
@media (max-width: 600px) {
  .shell > .welcome {
    margin-top: var(--s4);
  }
  .welcome .intro {
    margin-bottom: var(--s4);
  }
  .welcome .intro h1 {
    font-size: clamp(24px, 7vw, 32px);
    letter-spacing: -0.6px;
    margin: 0 0 var(--s2);
  }
  .welcome .intro p {
    font-size: var(--sm);
    line-height: 1.45;
    margin: 0;
  }
  .welcome .login-panel {
    padding: var(--s4);
  }
  .login-heading .section-icon {
    width: 36px;
    height: 36px;
  }
  .login-heading h2 {
    font-size: var(--lg);
  }
  .login-panel > p {
    margin: var(--s2) 0 0;
    line-height: 1.45;
  }
  .login-panel form {
    margin-top: var(--s4);
  }
  .login-panel form label {
    margin-bottom: var(--s3);
  }
}
.login-panel > p {
  font-size: var(--sm);
}
.remember-me input {
  width: 18px;
  height: 18px;
  min-height: 18px;
  margin: 0;
  padding: 0;
  accent-color: var(--accent);
}
form .remember-me {
  min-height: var(--control);
  display: flex;
  align-items: center;
  gap: var(--s3);
}
.password-field {
  position: relative;
  display: block;
  margin-top: var(--s2);
}
.password-field input {
  margin-top: 0;
  padding-right: calc(var(--control) + var(--s2));
}
.password-toggle {
  position: absolute;
  right: var(--s1);
  top: 50%;
  transform: translateY(-50%);
  color: var(--muted);
}
.code-help {
  display: grid;
  gap: var(--s5);
}
.code-help h3 {
  margin: 0 0 var(--s2);
  font-size: var(--md);
}
.code-help p {
  margin-bottom: var(--s3);
}
.code-help ol {
  margin: 0;
  padding-left: var(--s5);
  font-size: var(--sm);
}
.code-help li + li {
  margin-top: var(--s2);
}
.login-progress {
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--s3);
  color: var(--muted);
  font-size: var(--sm);
}
</style>
