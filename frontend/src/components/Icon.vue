<script setup lang="ts">
withDefaults(defineProps<{ name: string; size?: number }>(), { size: 20 })
const paths: Record<string, string> = {
  selectAll: 'M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Zm2 9 3 3 7-7',
  deselectAll: 'M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z',
  debug:
    'M8 9h8v8a4 4 0 0 1-8 0V9Zm2-4h4l2 4M8 9l2-4M3 13h5m8 0h5M4 6l4 4m8 0 4-4M4 20l4-3m8 0 4 3M12 9v12',
  settings: 'M4 7h16M4 17h16M9 4v6M15 14v6',
  minus: 'M5 12h14',
  plus: 'M5 12h14M12 5v14',
  up: 'm6 15 6-6 6 6',
  profile: 'M19 21v-2a7 7 0 0 0-14 0v2M16 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0',
  down: 'm6 9 6 6 6-6',
  sun: 'M12 3v1m0 16v1M3 12h1m16 0h1M5.6 5.6l.7.7m11.4 11.4.7.7m0-12.8-.7.7M6.3 17.7l-.7.7M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0',
  moon: 'M20.5 14A8.5 8.5 0 0 1 10 3.5 8.5 8.5 0 1 0 20.5 14Z',
  search: 'M21 21l-5-5M18 10.5a7.5 7.5 0 1 1-15 0 7.5 7.5 0 0 1 15 0',
  download: 'M12 3v12m-5-5 5 5 5-5M4 16v4h16v-4',
  upload: 'M12 15V3m-5 5 5-5 5 5M4 16v4h16v-4',
  import: 'M13 3H5v18h14V9l-6-6v6h6M9 15h6m-3-3v6',
  share:
    'M13.8 9.3a1.8 1.8 0 1 1-3.6 0 1.8 1.8 0 0 1 3.6 0ZM7.5 21l4.5-9.9 4.5 9.9M9.3 17.4h5.4M8.22 5.52a5.4 5.4 0 0 0 0 7.56m7.56-7.56a5.4 5.4 0 0 1 0 7.56M5.61 2.91a9 9 0 0 0 0 12.78m12.78-12.78a9 9 0 0 1 0 12.78',
  refresh: 'M20 8a8.5 8.5 0 1 0 0 8M20 3v5h-5',
  trash: 'M4 6h16M9 6V3h6v3M6 6l1 15h10l1-15M10 10v7m4-7v7',
  close: 'm6 6 12 12M6 18 18 6',
  stop: 'M6 6h12v12H6V6Z',
  back: 'm14 5-7 7 7 7',
  next: 'm10 5 7 7-7 7',
  more: 'M12 4h.01M12 12h.01M12 20h.01',
  check: 'm5 12 4 4L19 6',
  app: 'M7 4h10a3 3 0 0 1 3 3v10a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3V7a3 3 0 0 1 3-3Zm1 12 4-8 4 8m-7-3h6',
  library: 'M4 5v15M8 3v17M13 5v15M17 4l4 15',
  logout: 'M10 4H4v16h6m4-13 5 5-5 5m-6-5h11',
  arrow: 'M19 8H9a5 5 0 0 0 0 10h3M13 2l6 6-6 6',
  eye: 'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Zm13 0a3 3 0 1 1-6 0 3 3 0 0 1 6 0',
  eyeOff:
    'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Zm13 0a3 3 0 1 1-6 0 3 3 0 0 1 6 0M3 3l18 18',
  lock: 'M6 10h12v11H6V10Zm3 0V6a3 3 0 0 1 6 0v4',
  copy: 'M9 9h12v12H9V9ZM15 9V3H3v12h6',
}
</script>
<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.7"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path :d="paths[name] || paths.app" />
  </svg>
</template>
