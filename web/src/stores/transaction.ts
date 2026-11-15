import { defineStore } from 'pinia'
import {
  adjustTransaction,
  batchDeleteTransactions,
  createTransaction,
  getTransaction,
  listTransactions,
  purgeTransaction,
  restoreTransaction,
  softDeleteTransaction,
  updateTransaction,
} from '@/api/transactions'
import type {
  AdjustPayload,
  AdjustResult,
  TransactionCreatePayload,
  TransactionCreateResult,
  TransactionDTO,
  TransactionDetailDTO,
  TransactionQuery,
  TransactionSummary,
  TransactionUpdatePayload,
} from '@/api/types'

interface TransactionState {
  items: TransactionDTO[]
  total: number
  page: number
  pageSize: number
  sort: NonNullable<TransactionQuery['sort']>
  summary: TransactionSummary | null
  loading: boolean
  error: string | null
  query: TransactionQuery
  detail: TransactionDetailDTO | null
  detailLoading: boolean
  drawerOpen: boolean
  editing: TransactionDTO | null
  dialogOpen: boolean
  deletedItems: TransactionDTO[]
  deletedTotal: number
  recycleOpen: boolean
  /** 请求序号：丢弃乱序响应（快速翻页/切换筛选不串数据）。 */
  listSeq: number
}

export const useTransactionStore = defineStore('transaction', {
  state: (): TransactionState => ({
    items: [],
    total: 0,
    page: 1,
    pageSize: 50,
    sort: 'time_desc',
    summary: null,
    loading: false,
    error: null,
    query: {},
    detail: null,
    detailLoading: false,
    drawerOpen: false,
    editing: null,
    dialogOpen: false,
    deletedItems: [],
    deletedTotal: 0,
    recycleOpen: false,
    listSeq: 0,
  }),
  actions: {
    /** 拉取列表；reset=true 时页码归 1。乱序响应通过序号丢弃。 */
    async fetchList(reset = false): Promise<void> {
      if (reset) this.page = 1
      const seq = ++this.listSeq
      this.loading = true
      this.error = null
      try {
        const res = await listTransactions({
          ...this.query,
          page: this.page,
          page_size: this.pageSize,
          sort: this.sort,
        })
        if (seq !== this.listSeq) return // 已有更新的请求发出，丢弃旧响应
        this.items = res.items
        this.total = res.total
        this.summary = res.summary
      } catch (e) {
        if (seq !== this.listSeq) return
        this.error = e instanceof Error ? e.message : '加载失败'
        this.items = []
      } finally {
        if (seq === this.listSeq) this.loading = false
      }
    },
    setQuery(q: TransactionQuery): void {
      this.query = { ...q }
    },
    setPage(p: number): void {
      this.page = p
      void this.fetchList()
    },
    setPageSize(s: number): void {
      this.pageSize = s
      void this.fetchList(true)
    },
    setSort(s: TransactionState['sort']): void {
      this.sort = s
      void this.fetchList(true)
    },
    async fetchDetail(id: string): Promise<void> {
      this.drawerOpen = true
      this.detailLoading = true
      try {
        this.detail = await getTransaction(id)
      } finally {
        this.detailLoading = false
      }
    },
    closeDrawer(): void {
      this.drawerOpen = false
      this.detail = null
    },
    openEdit(txn: TransactionDTO): void {
      this.editing = txn
      this.dialogOpen = true
    },
    closeEdit(): void {
      this.dialogOpen = false
      this.editing = null
    },
    async create(payload: TransactionCreatePayload): Promise<TransactionCreateResult> {
      const res = await createTransaction(payload)
      void this.fetchList()
      return res
    },
    async update(id: string, payload: TransactionUpdatePayload): Promise<void> {
      await updateTransaction(id, payload)
      await this.fetchList()
    },
    async softDelete(id: string): Promise<void> {
      await softDeleteTransaction(id)
      this.drawerOpen = false
      await this.fetchList()
    },
    async restore(id: string): Promise<void> {
      await restoreTransaction(id)
      await Promise.all([this.fetchList(), this.fetchDeleted()])
    },
    async purge(id: string): Promise<void> {
      await purgeTransaction(id)
      await this.fetchDeleted()
    },
    async adjust(id: string, payload: AdjustPayload): Promise<AdjustResult> {
      const res = await adjustTransaction(id, payload)
      await this.fetchList()
      return res
    },
    async batchDelete(ids: string[]): Promise<{ succeeded: string[]; failed: { id: string; code: string }[] }> {
      const res = await batchDeleteTransactions(ids)
      await this.fetchList()
      return res
    },
    async fetchDeleted(): Promise<void> {
      const res = await listTransactions({
        ...this.query,
        include_deleted: true,
        page: 1,
        page_size: 200,
        sort: 'time_desc',
      })
      this.deletedItems = res.items.filter((t) => t.is_deleted)
      this.deletedTotal = this.deletedItems.length
    },
  },
})
