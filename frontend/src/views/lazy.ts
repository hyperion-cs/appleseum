export const loadProfile = () => import('./ProfileView.vue')

export const loadSettings = () => import('./SettingsView.vue')

export const loadDebug = () => import('./DebugView.vue')

export const loadDownloadDialog = () => import('./DownloadDialog.vue')

export const loadShareDialog = () => import('./ShareDialog.vue')

// Fetches a screen's code while the pointer is still on its way to the control.
export function warm(load: () => Promise<unknown>) {
  void load().catch(() => {})
}
