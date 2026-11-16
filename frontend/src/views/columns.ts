import { t } from '../i18n/index.ts'
import type { TableColumn } from '../components/table.ts'

export type AppColumn = 'name' | 'status' | 'version' | 'size' | 'purchasedAt' | 'createdAt'

export function appColumns(keys: AppColumn[], canSort: (key: string) => boolean): TableColumn[] {
  const labels: Record<AppColumn, string> = {
    name: t.value.name,
    status: t.value.status,
    version: t.value.version,
    size: t.value.size,
    purchasedAt: t.value.purchasedAt,
    createdAt: t.value.date,
  }
  return keys.map((key) => ({ key, label: labels[key], sortable: canSort(key) }))
}
