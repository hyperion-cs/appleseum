<script setup lang="ts">
import { computed, reactive, toRef, useId } from 'vue'
import type { State } from '../api/types.ts'
import { useDeviceSettings } from '../composables/useDeviceSettings.ts'
import { t, useErrorMessage } from '../i18n/index.ts'
import ChoiceSelect from '../components/ChoiceSelect.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import FieldHelp from '../components/FieldHelp.vue'
import Icon from '../components/Icon.vue'
import MessageDialog from '../components/MessageDialog.vue'
import { deviceOptions } from './deviceOptions.ts'

const props = defineProps<{ state: State }>()
const emit = defineEmits<{ changed: [] }>()
const inputId = useId()
const { code: errorCode } = useErrorMessage()
const device = reactive(useDeviceSettings(toRef(props, 'state'), errorCode, () => emit('changed')))
const choices = computed(() => ({
  ...deviceOptions(device.form ?? {}),
  model: device.models,
  osVersion: device.osVersions,
}))
const confirmations = computed(() => ({
  save: { title: t.value.deviceModelChange, action: t.value.deviceChange },
  defaults: { title: t.value.deviceDefaults, action: t.value.deviceRestore },
  reset: { title: t.value.deviceNew, action: t.value.deviceCreate },
}))

await device.load()
</script>

<template>
  <main class="workspace settings-panel">
    <MessageDialog
      v-if="errorCode && !device.confirming"
      :code="errorCode"
      @close="errorCode = ''"
    />
    <button
      v-if="device.loadFailed && !device.profile"
      :class="{ pending: device.loading }"
      :disabled="device.loading"
      :aria-busy="device.loading"
      @click="device.load"
    >
      <Icon name="refresh" :size="18" />
      {{ t.refresh }}
    </button>
    <section class="panel">
      <h2>{{ t.deviceTitle }}</h2>
      <p class="muted">{{ t.deviceDescription }}</p>
      <p v-if="device.profile?.readOnly" class="muted" role="status">
        {{ t.deviceAuthenticationRequired }}
      </p>
      <form
        v-if="device.profile && device.form"
        class="device-form"
        :aria-busy="device.saving"
        @submit.prevent="device.requestUpdate('save')"
      >
        <div class="device-section">
          <div class="device-fields">
            <div v-for="field in device.fields" :key="field.key" class="device-field">
              <div class="device-field-label">
                <label :for="inputId + '-' + field.key">{{ field.label }}</label>
                <FieldHelp :label="field.label" :text="field.help" />
              </div>
              <ChoiceSelect
                :id="inputId + '-' + field.key"
                v-model="device.form[field.key]"
                :label="field.label"
                :options="choices[field.key]"
                :disabled="device.saving || device.locked"
              />
            </div>
          </div>
        </div>
        <section class="device-section" :aria-labelledby="inputId + '-identity'">
          <h3 :id="inputId + '-identity'">{{ t.deviceIdentity }}</h3>
          <dl class="profile-details device-details">
            <div class="profile-field">
              <dt>
                {{ t.deviceId }}
                <FieldHelp :label="t.deviceId" :text="t.deviceIdHelp" />
              </dt>
              <dd class="device-identifier">{{ device.profile.id }}</dd>
            </div>
            <div class="profile-field">
              <dt>
                GUID
                <FieldHelp label="GUID" :text="t.deviceGuidHelp" />
              </dt>
              <dd class="device-identifier">{{ device.profile.guid }}</dd>
            </div>
            <div class="profile-field">
              <dt>
                {{ t.deviceSoftware }}
                <FieldHelp :label="t.deviceSoftware" :text="t.deviceSoftwareHelp" />
              </dt>
              <dd>{{ device.profile.clientVersion }}</dd>
            </div>
          </dl>
        </section>
        <footer class="device-footer">
          <div class="device-actions">
            <button
              type="submit"
              class="primary"
              :disabled="device.locked || device.saving || !device.dirty"
              :aria-busy="device.savingAction === 'save'"
            >
              <span v-if="device.savingAction === 'save'" class="spinner"></span>
              {{ t.saveSettings }}
            </button>
            <button
              type="button"
              :disabled="device.locked || device.saving || !device.defaultsChanged"
              :aria-busy="device.savingAction === 'defaults'"
              @click="device.requestUpdate('defaults')"
            >
              <span v-if="device.savingAction === 'defaults'" class="spinner"></span>
              {{ t.deviceDefaults }}
            </button>
            <button
              type="button"
              class="danger"
              :disabled="device.locked || device.saving"
              @click="device.confirm('reset')"
            >
              {{ t.deviceNew }}
            </button>
          </div>
          <p class="muted">{{ t.deviceDefaultsHint }}</p>
        </footer>
      </form>
      <p v-else-if="device.loading" role="status">
        <span class="spinner"></span>
        {{ t.settingsLoading }}
      </p>
    </section>
    <ConfirmDialog
      v-if="device.confirming"
      :busy="device.saving"
      :disabled="device.locked"
      :error="errorCode"
      :title="confirmations[device.confirming].title"
      :action="confirmations[device.confirming].action"
      @close="((device.confirming = ''), (errorCode = ''))"
      @confirm="device.confirming && device.update(device.confirming)"
    >
      <template v-if="device.confirming === 'defaults'">
        <p>{{ t.deviceDefaultsConfirm }}</p>
        <ul v-if="device.defaultChanges.length">
          <li v-for="change in device.defaultChanges" :key="change.key">
            {{ change.label }}: {{ change.value }}
          </li>
        </ul>
        <p v-if="device.defaultsReplaceIdentity">{{ t.deviceModelChangeConfirm }}</p>
      </template>
      <p v-else>
        {{ device.confirming === 'reset' ? t.deviceNewConfirm : t.deviceModelChangeConfirm }}
      </p>
      <p
        v-if="state.account && (device.confirming !== 'defaults' || device.defaultsReplaceIdentity)"
      >
        {{ t.deviceSessionsEnd }}
      </p>
    </ConfirmDialog>
  </main>
</template>

<style>
.settings-panel .panel {
  padding: var(--s4);
}
.settings-panel h2 {
  margin-top: 0;
}
.settings-panel p {
  margin: var(--s2) 0;
  font-size: var(--sm);
}
.device-form {
  margin: 0;
}
.device-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--s2) var(--s4);
  border: 0;
  padding: 0;
  margin: 0;
  min-width: 0;
}
.device-field {
  display: flex;
  flex-direction: column;
  gap: 0;
  min-width: 0;
}
.device-field-label,
.device-details dt {
  display: flex;
  align-items: center;
  gap: var(--s1);
}
.device-field-label label {
  margin: 0;
}
.device-section + .device-section {
  border-top: 1px solid var(--subtle-line);
  margin-top: var(--s4);
  padding-top: var(--s3);
}
.device-section h3 {
  margin: 0;
  font-size: var(--md);
}
.device-details .profile-field {
  align-items: center;
}
.device-details {
  margin: 0;
  gap: var(--s2) var(--s4);
}
.device-identifier {
  font-family: ui-monospace, monospace;
  font-size: var(--sm);
  overflow-wrap: anywhere;
}
@media (min-width: 960px) {
  .device-details {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
.device-footer {
  border-top: 1px solid var(--subtle-line);
  margin-top: var(--s4);
  padding-top: var(--s4);
}
.device-footer > p {
  max-width: 65ch;
  margin: var(--s3) auto 0;
  text-align: center;
}
.device-actions {
  display: flex;
  flex-direction: column;
  gap: var(--s3);
  max-width: var(--narrow);
  margin-inline: auto;
}
.device-actions > button {
  min-width: 0;
  white-space: normal;
}
@media (min-width: 1200px) {
  .device-fields {
    grid-template-columns: minmax(0, 1.6fr) repeat(3, minmax(0, 1fr));
  }
}
@media (min-width: 960px) {
  .device-actions {
    flex-direction: row;
    flex-wrap: wrap;
    justify-content: center;
    max-width: none;
  }
}
@media (max-width: 600px) {
  .device-fields {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
