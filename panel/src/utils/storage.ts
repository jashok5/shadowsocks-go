const TOKEN_KEY = 'panel_token'
const EXPIRES_AT_KEY = 'panel_token_expires_at'

export function readToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function readTokenExpiresAt(): number {
  const raw = localStorage.getItem(EXPIRES_AT_KEY)
  if (!raw) return 0
  const parsed = Number(raw)
  if (!Number.isFinite(parsed) || parsed <= 0) return 0
  return parsed
}

export function saveToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
}

export function saveTokenExpiresAt(expiresAt: number): void {
  localStorage.setItem(EXPIRES_AT_KEY, String(expiresAt))
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(EXPIRES_AT_KEY)
}
