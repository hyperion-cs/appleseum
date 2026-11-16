<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { DownloadVersion } from '../api/types.ts'
import { useVersionPicker } from '../composables/useVersionPicker.ts'
import { t, errorText } from '../i18n/index.ts'
import type { OsCompatibility } from '../platform/device.ts'
import ChoiceSelect from '../components/ChoiceSelect.vue'
import DataTable from '../components/DataTable.vue'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import TableRow from '../components/TableRow.vue'

const props = defineProps<{
  ids: string[]
  names: Record<string, string>
  busy: boolean
  error: string
}>()
const emit = defineEmits<{ close: []; confirm: [versions: DownloadVersion[]]; expired: [] }>()
const picker = reactive(useVersionPicker(props.ids, props.names, () => emit('expired')))
const columns = computed(() => [
  { key: 'version', label: t.value.appVersion },
  { key: 'minimumOs', label: t.value.minimumOs },
])
const compatibilityTitles = computed<Record<OsCompatibility, string | undefined>>(() => ({
  compatible: t.value.osCompatible,
  incompatible: t.value.osIncompatible,
  unknown: undefined,
}))
const appOptions = computed(() =>
  props.ids.map((id) => ({ value: id, label: picker.names[id] || id })),
)
</script>

<template>
  <Modal :title="t.download" :dismissible="!busy" @close="emit('close')">
    <div class="download-picker">
      <strong v-if="ids.length === 1" class="download-app-name">
        {{ picker.names[picker.appId] || picker.appId }}
      </strong>
      <fieldset class="download-mode" :disabled="busy" :aria-label="t.downloadVersion">
        <label>
          <input
            v-model="picker.specific"
            type="radio"
            :value="false"
            name="download-version-mode"
          />
          {{ t.latestVersion }}
        </label>
        <label>
          <input
            v-model="picker.specific"
            type="radio"
            :value="true"
            name="download-version-mode"
          />
          {{ t.specificVersion }}
        </label>
      </fieldset>
      <template v-if="picker.specific">
        <ChoiceSelect
          v-if="ids.length > 1"
          v-model="picker.appId"
          :options="appOptions"
          :label="t.name"
          :disabled="busy"
        />
        <div class="version-selection">
          <span>
            {{ t.selectedVersion }}:
            <strong>{{ picker.choices[picker.appId]?.label ?? t.versionNotSelected }}</strong>
          </span>
          <button
            v-if="picker.choices[picker.appId]"
            :disabled="busy"
            @click="delete picker.choices[picker.appId]"
          >
            {{ t.clearVersion }}
          </button>
        </div>
        <p v-if="ids.length > 1" class="muted">{{ t.otherAppsLatest }}</p>
        <DataTable
          :columns="columns"
          class="version-table"
          :aria-label="t.specificVersion"
          :aria-busy="picker.loading"
          :inert="busy"
          :responsive="false"
          :loading="picker.loading"
          :empty="!!picker.errorCode || !picker.page || !picker.versions.length"
          :empty-text="t.noValue"
          wrapper-class="version-table-wrap"
          scrollable
          :pagination="{
            size: picker.limit,
            total: picker.page?.total,
            previous: picker.page?.previous,
            next: picker.page?.next,
            disabled: picker.loading || busy,
            pending: picker.navigating,
          }"
          @page-size="picker.choosePageSize"
          @navigate="picker.navigate"
        >
          <template #loading>
            <span class="version-loading">
              <span
                role="progressbar"
                :aria-label="t.loadingVersions"
                :aria-valuenow="picker.progress"
                :aria-valuemin="0"
                :aria-valuemax="100"
              >
                <span class="spinner" aria-hidden="true"></span>
                {{ t.loadingVersions }} {{ picker.progress }}%
              </span>
            </span>
          </template>
          <TableRow
            v-for="item in picker.versions"
            :key="item.id"
            :selected="picker.choices[picker.appId]?.id === item.id"
          >
            <td>
              <label class="version-option">
                <input
                  type="radio"
                  :name="'version-' + picker.appId"
                  :checked="picker.choices[picker.appId]?.id === item.id"
                  :disabled="!!item.error || !item.version || picker.loading || busy"
                  @change="picker.choices[picker.appId] = { id: item.id, label: item.version }"
                />
                <span>
                  {{ item.error ? errorText(item.error) : item.version }}
                  <small v-if="item.latest">{{ t.latestVersion }}</small>
                </span>
              </label>
            </td>
            <td>
              <span
                class="minimum-os"
                :class="'os-' + item.compatibility"
                :title="compatibilityTitles[item.compatibility]"
              >
                {{ item.minimumOs ? item.minimumOs + '+' : t.noValue }}
                <Icon
                  v-if="item.compatibility !== 'unknown'"
                  :name="item.compatibility === 'compatible' ? 'check' : 'close'"
                  :size="14"
                />
              </span>
            </td>
          </TableRow>
        </DataTable>
        <div v-if="picker.failed" class="version-error" role="alert">
          <p v-if="picker.errorCode">{{ errorText(picker.errorCode) }}</p>
          <button :disabled="picker.loading || busy" @click="picker.retry()">
            {{ t.retryVersions }}
          </button>
        </div>
      </template>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <div class="modal-actions">
        <button :disabled="busy" @click="emit('close')">{{ t.cancel }}</button>
        <button
          class="primary"
          :disabled="busy || (picker.specific && !Object.keys(picker.choices).length)"
          :aria-busy="busy"
          @click="emit('confirm', picker.finish())"
        >
          <span v-if="busy" class="spinner"></span>
          <Icon v-else name="download" :size="18" />
          {{ t.download }}
        </button>
      </div>
    </div>
  </Modal>
</template>
<style>
.download-picker {
  display: grid;
  gap: var(--s4);
}
.download-app-name {
  overflow-wrap: anywhere;
}
.download-mode {
  display: grid;
  gap: var(--s2);
  padding: 0;
  margin: 0;
  border: 0;
}
.download-mode label,
.version-option {
  display: flex;
  align-items: center;
  gap: var(--s3);
  min-height: 44px;
  cursor: pointer;
}
.download-picker input[type='radio'] {
  width: 18px;
  height: 18px;
  min-height: 18px;
  margin: 0;
  padding: 0;
  border-radius: 50%;
  flex-shrink: 0;
  accent-color: var(--accent);
}
.version-selection {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s2);
  font-size: var(--sm);
}
.version-selection button {
  font-size: var(--sm);
}
.version-table-wrap {
  overflow: auto;
}
.version-table th:first-child {
  width: 60%;
}
.version-table th:nth-child(2) {
  width: 40%;
}
.version-table th {
  white-space: normal;
}
.version-table td:nth-child(2) {
  font-variant-numeric: tabular-nums;
}
.minimum-os {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--s1);
}
.os-compatible {
  color: var(--success);
}
.os-incompatible {
  color: var(--danger);
}
.version-option {
  font-size: var(--sm);
}
.version-option small {
  display: block;
  color: var(--muted);
}
.version-loading {
  padding: var(--s3);
  font-size: var(--sm);
  color: var(--muted);
}
.version-loading .spinner {
  display: inline-block;
  margin-right: var(--s2);
}
.version-error {
  font-size: var(--sm);
}
.download-picker .muted {
  color: var(--muted);
}
.download-picker .page-size {
  flex-wrap: wrap;
}
@media (min-width: 601px) and (hover: hover) and (pointer: fine) {
  .modal:has(.download-picker) {
    --control: 36px;
    max-width: 440px;
  }
  .modal:has(.download-picker) .modal-header {
    padding: var(--s3) var(--s4);
  }
  .modal:has(.download-picker) .modal-body {
    padding: var(--s4);
  }
  .modal:has(.download-picker) button {
    padding: 6px var(--s3);
    font-size: var(--sm);
  }
  .modal:has(.download-picker) .icon-button {
    padding: 6px;
  }
  .download-picker {
    gap: var(--s3);
  }
  .download-mode {
    gap: var(--s1);
  }
  .download-mode label {
    min-height: 28px;
    gap: var(--s2);
    font-size: var(--sm);
  }
  .download-picker .modal-actions {
    gap: var(--s2);
    margin-top: var(--s1);
  }
  .version-option {
    min-height: 32px;
  }
  .version-table th,
  .version-table td {
    padding-block: 6px;
  }
}
</style>
