export interface VPNClient {
  common_name: string
  real_addr: string
  virtual_ip: string
  connected_at: string
  bytes_recv: number
  bytes_sent: number
  hostname?: string
}

export interface BlockedEntry {
  ip: string
  blocked_at: string
}

export interface Instance {
  id: string
  name: string
  subnet: string
  status: string
  created_at: string
  clients: VPNClient[]
  client_timeouts?: Record<string, number>
  global_timeout: number
  max_clients: number        // per-instance override (0 = use global)
  global_max_clients: number // global config value
  active_conns: number
}

const TOKEN_KEY = 'otiv_auth_token'
const ROLE_KEY = 'otiv_auth_role'

export const auth = {
  getToken: (): string | null => sessionStorage.getItem(TOKEN_KEY),
  getRole: (): string | null => sessionStorage.getItem(ROLE_KEY),
  isAuthenticated: (): boolean => !!sessionStorage.getItem(TOKEN_KEY),
  isAdmin: (): boolean => sessionStorage.getItem(ROLE_KEY) === 'admin',
  save: (token: string, role: string) => {
    sessionStorage.setItem(TOKEN_KEY, token)
    sessionStorage.setItem(ROLE_KEY, role)
  },
  clear: () => {
    sessionStorage.removeItem(TOKEN_KEY)
    sessionStorage.removeItem(ROLE_KEY)
  },
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = auth.getToken()
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string>),
  }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  const res = await fetch(path, { ...init, headers })
  if (res.status === 401 || res.status === 403) {
    if (res.status === 401) {
      auth.clear()
    }
    const text = await res.text()
    throw new Error(text || res.statusText)
  }
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const api = {
  login: (password: string) =>
    fetch('/api/auth', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password }),
    }).then(async res => {
      if (!res.ok) {
        const text = await res.text()
        throw new Error(text || res.statusText)
      }
      return res.json() as Promise<{ token: string; role: string }>
    }),

  listInstances: () =>
    request<Instance[]>('/api/instances'),

  createInstance: (name: string) =>
    request<Instance>('/api/instances', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    }),

  deleteInstance: (id: string) =>
    request<void>(`/api/instances/${id}`, { method: 'DELETE' }),

  stopInstance: (id: string) =>
    request<void>(`/api/instances/${id}/stop`, { method: 'POST' }),

  startInstance: (id: string) =>
    request<void>(`/api/instances/${id}/start`, { method: 'POST' }),

  getClients: (id: string) =>
    request<VPNClient[]>(`/api/instances/${id}/clients`),

  kickClient: (id: string, cn: string) =>
    request<void>(`/api/instances/${id}/clients/${encodeURIComponent(cn)}/kick`, { method: 'POST' }),

  setHostname: (id: string, cn: string, hostname: string) =>
    request<void>(`/api/instances/${id}/hostnames/${encodeURIComponent(cn)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ hostname }),
    }),

  clientConfigUrl: (id: string) => `/api/instances/${id}/client-config`,

  listBlocked: () =>
    request<BlockedEntry[]>('/api/blocked'),

  blockIP: (ip: string) =>
    request<void>('/api/blocked', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ip }),
    }),

  unblockIP: (ip: string) =>
    request<void>(`/api/blocked/${encodeURIComponent(ip)}`, { method: 'DELETE' }),

  banClient: (ip: string) =>
    request<void>('/api/blocked', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ip }),
    }),

  setMaxClients: (id: string, max: number) =>
    request<void>(`/api/instances/${id}/max-clients`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ max }),
    }),

  setClientTimeout: (id: string, cn: string, seconds: number) =>
    request<void>(`/api/instances/${id}/clients/${encodeURIComponent(cn)}/timeout`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seconds }),
    }),
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
