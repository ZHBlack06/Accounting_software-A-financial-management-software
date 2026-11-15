import { defineStore } from 'pinia'
import { login as apiLogin, checkAuth } from '@/api/auth'

interface AuthState {
  token: string | null
  checked: boolean
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({
    token: localStorage.getItem('token'),
    checked: false,
  }),
  actions: {
    /** 登录：校验 token 后落 localStorage。 */
    async login(token: string): Promise<boolean> {
      const res = await apiLogin(token)
      if (res.ok) {
        this.token = token
        localStorage.setItem('token', token)
        this.checked = true
        return true
      }
      return false
    },
    logout(): void {
      this.token = null
      this.checked = false
      localStorage.removeItem('token')
    },
    /** 每会话一次的服务端校验。 */
    async check(): Promise<boolean> {
      try {
        const res = await checkAuth()
        this.checked = true
        return res.ok
      } catch {
        this.checked = false
        this.logout()
        return false
      }
    },
  },
})
