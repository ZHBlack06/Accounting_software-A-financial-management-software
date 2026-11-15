import { defineStore } from 'pinia'
import { fetchSettings, updateSettings } from '@/api/meta'
import type { QuickTemplate, TransactionDTO } from '@/api/types'

/** 内置常用模板（PRD 5.5），settings.quick_templates 为空时作为兜底展示。 */
export const BUILTIN_TEMPLATES: QuickTemplate[] = [
  { id: 'tpl_breakfast', name: '早餐', amount_cents: 1000, account_id: null, category_id: null, merchant_id: null, sort_order: 0 },
  { id: 'tpl_lunch', name: '午餐', amount_cents: 2500, account_id: null, category_id: null, merchant_id: null, sort_order: 1 },
  { id: 'tpl_dinner', name: '晚餐', amount_cents: 3000, account_id: null, category_id: null, merchant_id: null, sort_order: 2 },
  { id: 'tpl_metro', name: '地铁', amount_cents: 400, account_id: null, category_id: null, merchant_id: null, sort_order: 3 },
  { id: 'tpl_bus', name: '公交', amount_cents: 200, account_id: null, category_id: null, merchant_id: null, sort_order: 4 },
  { id: 'tpl_taxi', name: '打车', amount_cents: 2500, account_id: null, category_id: null, merchant_id: null, sort_order: 5 },
  { id: 'tpl_coffee', name: '咖啡', amount_cents: 1800, account_id: null, category_id: null, merchant_id: null, sort_order: 6 },
  { id: 'tpl_takeout', name: '外卖', amount_cents: 3000, account_id: null, category_id: null, merchant_id: null, sort_order: 7 },
  { id: 'tpl_market', name: '超市', amount_cents: 8000, account_id: null, category_id: null, merchant_id: null, sort_order: 8 },
  { id: 'tpl_fuel', name: '加油', amount_cents: 30000, account_id: null, category_id: null, merchant_id: null, sort_order: 9 },
]

interface DraftState {
  lastAccountId: string | null
  lastCategoryId: string | null
  lastSubCategoryId: string | null
  lastMerchantId: string | null
  templates: QuickTemplate[]
  templatesLoaded: boolean
}

function load(key: string): string | null {
  return localStorage.getItem(key)
}

export const useDraftStore = defineStore('draft', {
  state: (): DraftState => ({
    lastAccountId: load('draft.accountId'),
    lastCategoryId: load('draft.categoryId'),
    lastSubCategoryId: load('draft.subCategoryId'),
    lastMerchantId: load('draft.merchantId'),
    templates: [],
    templatesLoaded: false,
  }),
  getters: {
    /** 模板列表：服务端优先，空则用内置模板。 */
    effectiveTemplates(state): QuickTemplate[] {
      if (state.templates.length > 0) {
        return [...state.templates].sort((a, b) => a.sort_order - b.sort_order)
      }
      return BUILTIN_TEMPLATES
    },
  },
  actions: {
    async loadTemplates(): Promise<void> {
      try {
        const s = await fetchSettings()
        this.templates = s.quick_templates ?? []
      } catch {
        this.templates = []
      } finally {
        this.templatesLoaded = true
      }
    },
    /** 保存成功后记忆"上次使用"（落 localStorage）。 */
    remember(created: TransactionDTO): void {
      if (created.account?.id) {
        this.lastAccountId = created.account.id
        localStorage.setItem('draft.accountId', created.account.id)
      }
      if (created.category?.id) {
        this.lastCategoryId = created.category.id
        localStorage.setItem('draft.categoryId', created.category.id)
      }
      this.lastSubCategoryId = created.sub_category?.id ?? null
      if (created.sub_category?.id) localStorage.setItem('draft.subCategoryId', created.sub_category.id)
      this.lastMerchantId = created.merchant?.id ?? null
      if (created.merchant?.id) localStorage.setItem('draft.merchantId', created.merchant.id)
    },
    async saveTemplates(templates: QuickTemplate[]): Promise<void> {
      this.templates = templates
      await updateSettings({ quick_templates: templates })
    },
  },
})
