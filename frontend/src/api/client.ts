export interface VPNClient {
  common_name: string
  real_addr: string
  virtual_ip: string
  connected_at: string
  bytes_recv: number
  bytes_sent: number
  hostname?: string
}

export interface Instance {
  id: string
  name: string
  subnet: string
  status: string
  created_at: string
  clients: VPNClient[]
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init)
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const api = {
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
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
