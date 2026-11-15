import { defineStore } from 'pinia'
import { listAccounts } from '@/api/accounts'
import { listCategories } from '@/api/categories'
import { listTags } from '@/api/tags'
import { searchMerchants } from '@/api/merchants'
import { listLedgers } from '@/api/ledgers'
import type {
  AccountDTO,
  CategoryDTO,
  CategoryNode,
  LedgerDTO,
  MerchantDTO,
  RegionDTO,
  TagDTO,
} from '@/api/types'

interface MasterState {
  accounts: AccountDTO[]
  totalBalanceCents: number
  categories: CategoryNode[]
  tags: TagDTO[]
  merchants: MerchantDTO[]
  ledgers: LedgerDTO[]
  regionsCache: Record<string, RegionDTO[]>
  loaded: { accounts: boolean; categories: boolean; tags: boolean; ledgers: boolean }
}

export const useMasterStore = defineStore('master', {
  state: (): MasterState => ({
    accounts: [],
    totalBalanceCents: 0,
    categories: [],
    tags: [],
    merchants: [],
    ledgers: [],
    regionsCache: {},
    loaded: { accounts: false, categories: false, tags: false, ledgers: false },
  }),
  getters: {
    activeAccounts(state): AccountDTO[] {
      return state.accounts.filter((a) => !a.is_archived)
    },
    flatCategories(state): CategoryDTO[] {
      const out: CategoryDTO[] = []
      for (const c of state.categories) {
        out.push(c)
        for (const child of c.children ?? []) out.push(child)
      }
      return out
    },
    expenseCategories(state): CategoryNode[] {
      return state.categories.filter((c) => c.transaction_type === 'expense')
    },
    incomeCategories(state): CategoryNode[] {
      return state.categories.filter((c) => c.transaction_type === 'income')
    },
    categoryColor(): (id: string | null | undefined) => string | null {
      return (id) => {
        if (!id) return null
        const found = (this as { flatCategories: CategoryDTO[] }).flatCategories.find((c) => c.id === id)
        return found?.color ?? null
      }
    },
    categoryName(): (id: string | null | undefined) => string {
      return (id) => {
        if (!id) return ''
        const found = (this as { flatCategories: CategoryDTO[] }).flatCategories.find((c) => c.id === id)
        return found?.name ?? ''
      }
    },
  },
  actions: {
    subCategoriesOf(id: string | null | undefined): CategoryDTO[] {
      if (!id) return []
      return this.categories.find((c) => c.id === id)?.children ?? []
    },
    async loadAccounts(): Promise<void> {
      const res = await listAccounts(false, true)
      this.accounts = res.items
      this.totalBalanceCents = res.total_balance_cents
      this.loaded.accounts = true
    },
    async loadCategories(): Promise<void> {
      const res = await listCategories(undefined, true)
      this.categories = res.items
      this.loaded.categories = true
    },
    async loadTags(): Promise<void> {
      const res = await listTags()
      this.tags = res.items
      this.loaded.tags = true
    },
    async loadLedgers(): Promise<void> {
      const res = await listLedgers()
      this.ledgers = res.items
      this.loaded.ledgers = true
    },
    async searchMerchantOptions(q: string): Promise<void> {
      const res = await searchMerchants(q, '', 20)
      this.merchants = res.items
    },
    /** 启动时一次性加载全部主数据（失败不阻塞，各区块自行显示错误态）。 */
    async loadAll(): Promise<void> {
      await Promise.allSettled([
        this.loadAccounts(),
        this.loadCategories(),
        this.loadTags(),
        this.loadLedgers(),
        this.searchMerchantOptions(''),
      ])
    },
    async refresh(kind: 'accounts' | 'categories' | 'tags' | 'ledgers'): Promise<void> {
      if (kind === 'accounts') return this.loadAccounts()
      if (kind === 'categories') return this.loadCategories()
      if (kind === 'tags') return this.loadTags()
      return this.loadLedgers()
    },
  },
})
