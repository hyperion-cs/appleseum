import type { PageDirection } from '../api/pagination.ts'

export interface TableColumn {
  key: string
  label: string
  sortable?: boolean
  class?: string
  title?: string
}

export interface TableSelection {
  checked: boolean
  partial: boolean
  disabled?: boolean
}

export interface TablePagination {
  size: number
  total?: number
  previous?: boolean
  next?: boolean
  disabled?: boolean
  pending?: PageDirection | ''
}
