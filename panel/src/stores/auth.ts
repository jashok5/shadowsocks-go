import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { verifyToken } from '../api/panel'
import {
  clearToken,
  readToken,
  readTokenExpiresAt,
  saveToken,
  saveTokenExpiresAt,
} from '../utils/storage'

const ONE_HOUR_MS = 60 * 60 * 1000

export const useAuthStore = defineStore('auth', () => {
  const token = ref('')
  const checking = ref(false)
  const expiresAt = ref(0)
  let expiryTimer: number | null = null

  hydrate()

  const isLoggedIn = computed(() => token.value.length > 0 && Date.now() < expiresAt.value)

  async function login(inputToken: string): Promise<void> {
    const trimmed = inputToken.trim()
    token.value = trimmed
    saveToken(trimmed)
    expiresAt.value = Date.now() + ONE_HOUR_MS
    saveTokenExpiresAt(expiresAt.value)
    scheduleExpiryTimer()
    checking.value = true
    try {
      await verifyToken()
    } catch (error) {
      logout()
      throw error
    } finally {
      checking.value = false
    }
  }

  function logout(): void {
    token.value = ''
    expiresAt.value = 0
    clearExpiryTimer()
    clearToken()
  }

  function hydrate(): void {
    const storedToken = readToken()
    const storedExpires = readTokenExpiresAt()
    if (!storedToken || !storedExpires || Date.now() >= storedExpires) {
      clearToken()
      token.value = ''
      expiresAt.value = 0
      return
    }
    token.value = storedToken
    expiresAt.value = storedExpires
    scheduleExpiryTimer()
  }

  function scheduleExpiryTimer(): void {
    clearExpiryTimer()
    if (!expiresAt.value) return
    const delay = expiresAt.value - Date.now()
    if (delay <= 0) {
      logout()
      return
    }
    expiryTimer = window.setTimeout(() => {
      logout()
    }, delay)
  }

  function clearExpiryTimer(): void {
    if (expiryTimer !== null) {
      window.clearTimeout(expiryTimer)
      expiryTimer = null
    }
  }

  return {
    token,
    checking,
    expiresAt,
    isLoggedIn,
    login,
    logout,
    hydrate,
  }
})
