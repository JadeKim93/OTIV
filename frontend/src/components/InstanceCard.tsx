import { useState, useEffect, useRef } from 'react'
import { Instance, VPNClient, api, fmtBytes } from '../api/client'

function wsUrl(instanceId: string): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}/vpn/${instanceId}`
}

const COMMANDS = (instanceId: string) => {
  const url = wsUrl(instanceId)
  return [
    { tag: 'connect', cmd: `sudo otiv-client connect ${url}` },
    { tag: 'proxy',   cmd: `otiv-client proxy ${url}` },
    { tag: 'dns list',  cmd: `otiv-client dns list ${url}` },
    { tag: 'dns apply', cmd: `sudo otiv-client dns apply ${url}` },
  ]
}

function downloadYAML(instanceId: string) {
  const url = wsUrl(instanceId)
  const content = `url: ${url}\nport: "11194"\n`
  const blob = new Blob([content], { type: 'text/yaml' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `otiv-${instanceId.slice(0, 8)}.yaml`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(a.href)
}

function copyText(text: string) {
  navigator.clipboard.writeText(text)
}

interface Props {
  instance: Instance
  onDelete: (id: string) => void
  isAdmin: boolean
  onTimeoutChange?: (instanceId: string, cn: string, seconds: number) => void
}

export default function InstanceCard({ instance, onDelete, isAdmin, onTimeoutChange }: Props) {
  const [clients, setClients] = useState<VPNClient[]>(instance.clients ?? [])
  const [status, setStatus] = useState(instance.status)
  const [toggling, setToggling] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [copied, setCopied] = useState<number | null>(null)
  const [connectOpen, setConnectOpen] = useState(false)
  const pollingRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => { setStatus(instance.status) }, [instance.status])

  useEffect(() => {
    if (status !== 'running') {
      setClients([])
      return
    }
    let active = true
    async function poll() {
      try {
        const data = await api.getClients(instance.id)
        if (active) setClients(data ?? [])
      } catch {
        // management interface not ready yet — ignore
      }
    }
    poll()
    pollingRef.current = setInterval(poll, 200)
    return () => {
      active = false
      if (pollingRef.current) clearInterval(pollingRef.current)
    }
  }, [status, instance.id])

  async function handleToggle() {
    setToggling(true)
    try {
      if (status === 'running') {
        await api.stopInstance(instance.id)
        setStatus('stopped')
      } else {
        await api.startInstance(instance.id)
        setStatus('running')
      }
    } catch (e) {
      alert(String(e))
    } finally {
      setToggling(false)
    }
  }

  async function handleDelete() {
    if (!confirm(`Delete "${instance.name}"?`)) return
    setDeleting(true)
    try {
      await api.deleteInstance(instance.id)
      onDelete(instance.id)
    } catch (e) {
      alert(String(e))
      setDeleting(false)
    }
  }

  function handleCopy(idx: number, text: string) {
    copyText(text)
    setCopied(idx)
    setTimeout(() => setCopied(null), 2000)
  }

  const createdAt = new Date(instance.created_at).toLocaleString()
  const isRunning = status === 'running'
  const commands = COMMANDS(instance.id)

  return (
    <div style={styles.card} className="instance-card">
      <div style={styles.header}>
        <div>
          <div style={styles.name}>{instance.name}</div>
          <div style={styles.meta}>
            <span style={statusBadge(status)}>{status}</span>
            <span style={styles.dim}>subnet: {instance.subnet}/24</span>
            <span style={styles.dim}>created: {createdAt}</span>
            <MaxClientsDisplay instance={instance} isAdmin={isAdmin} />
          </div>
        </div>
        <div style={styles.actions}>
          <button
            onClick={handleToggle}
            disabled={toggling}
            style={{ background: isRunning ? '#b45309' : '#16a34a', color: '#fff' }}
          >
            {toggling ? '...' : isRunning ? 'Stop' : 'Resume'}
          </button>
          <button
            onClick={handleDelete}
            disabled={deleting}
            style={{ background: '#dc2626', color: '#fff' }}
          >
            {deleting ? '...' : 'Delete'}
          </button>
        </div>
      </div>

      <div style={styles.section}>
        <div style={styles.sectionTitle}>
          Connected clients ({clients.length})
        </div>
        {clients.length === 0 ? (
          <div style={styles.empty}>
            {isRunning ? 'No clients connected' : 'Instance stopped'}
          </div>
        ) : (
          <table style={styles.table}>
            <thead>
              <tr>
                {['Hostname', 'Common Name', 'Virtual IP', ...(isAdmin ? ['Remote IP'] : []), 'Sent', 'Received', 'Connected Since', 'Timeout', ''].map(h => (
                  <th key={h} style={styles.th}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {clients.map((c, i) => {
                const perClientTimeout = instance.client_timeouts?.[c.common_name] ?? 0
                const effectiveTimeout = perClientTimeout > 0 ? perClientTimeout : instance.global_timeout
                const connectedMs = c.connected_at ? new Date(c.connected_at).getTime() : 0
                const elapsedSec = connectedMs > 0 ? Math.floor((Date.now() - connectedMs) / 1000) : 0
                const remainingSec = effectiveTimeout > 0 ? effectiveTimeout - elapsedSec : -1
                return (
                  <tr key={i} className="client-row">
                    <td style={styles.td}>
                      <HostnameCell
                        hostname={c.hostname ?? ''}
                        onSave={name => api.setHostname(instance.id, c.common_name, name).catch(e => alert(String(e)))}
                      />
                    </td>
                    <td style={{ ...styles.td, color: '#6b7280', fontSize: 11 }}>{c.common_name}</td>
                    <td style={styles.td}>{c.virtual_ip}</td>
                    {isAdmin && <td style={{ ...styles.td, color: '#6b7280', fontSize: 11 }}>{c.real_addr}</td>}
                    <td style={styles.td}>{fmtBytes(c.bytes_sent)}</td>
                    <td style={styles.td}>{fmtBytes(c.bytes_recv)}</td>
                    <td style={styles.td}>
                      {c.connected_at ? new Date(c.connected_at).toLocaleString() : '-'}
                    </td>
                    <td style={styles.td}>
                      <TimeoutCell
                        seconds={perClientTimeout}
                        globalTimeout={instance.global_timeout}
                        editable={isAdmin}
                        remaining={remainingSec}
                        onSave={secs => {
                          api.setClientTimeout(instance.id, c.common_name, secs).then(() => {
                            onTimeoutChange?.(instance.id, c.common_name, secs)
                          }).catch(e => alert(String(e)))
                        }}
                      />
                    </td>
                    <td style={styles.td}>
                      {isAdmin && (
                        <div style={{ display: 'flex', gap: 4 }}>
                          <button
                            onClick={() => api.kickClient(instance.id, c.common_name).catch(e => alert(String(e)))}
                            style={{ background: '#7c3aed', color: '#fff', fontSize: 11, padding: '2px 8px' }}
                          >
                            Kick
                          </button>
                          {c.real_addr && (
                            <button
                              onClick={() => {
                                if (!confirm(`IP 차단: ${c.real_addr}\n해당 IP의 모든 연결이 즉시 차단됩니다.`)) return
                                api.banClient(c.real_addr).catch(e => alert(String(e)))
                              }}
                              style={{ background: '#dc2626', color: '#fff', fontSize: 11, padding: '2px 8px' }}
                            >
                              Ban
                            </button>
                          )}
                        </div>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>

      <div style={styles.section}>
        <div
          className="section-toggle"
          style={{ ...styles.sectionTitle, cursor: 'pointer', userSelect: 'none', display: 'flex', alignItems: 'center', gap: 6 }}
          onClick={() => setConnectOpen(o => !o)}
        >
          <span style={{ fontSize: 10, color: '#6b7280' }}>{connectOpen ? '▼' : '▶'}</span>
          Connect
        </div>
        {connectOpen && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 8 }}>
            {commands.map(({ tag, cmd }, idx) => (
              <div key={tag}>
                <div style={styles.cmdLabel}>
                  <span style={styles.cmdTag}>{tag}</span>
                  <button
                    onClick={() => handleCopy(idx, cmd)}
                    style={{ ...styles.copyBtn, background: copied === idx ? '#16a34a' : '#374151' }}
                  >
                    {copied === idx ? 'Copied!' : 'Copy'}
                  </button>
                </div>
                <code style={styles.code}>{cmd}</code>
              </div>
            ))}
            <div style={{ display: 'flex', gap: 8, marginTop: 4 }}>
              <a href={api.clientConfigUrl(instance.id)} download>
                <button style={{ background: '#1f2937', color: '#9ca3af', fontSize: 12 }}>
                  Download .ovpn
                </button>
              </a>
              <button
                onClick={() => downloadYAML(instance.id)}
                style={{ background: '#1f2937', color: '#9ca3af', fontSize: 12 }}
              >
                Download config.yaml
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function MaxClientsDisplay({ instance, isAdmin }: { instance: Instance; isAdmin: boolean }) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(instance.max_clients > 0 ? String(instance.max_clients) : '')

  const effectiveMax = instance.max_clients > 0 ? instance.max_clients : instance.global_max_clients
  const label = effectiveMax > 0
    ? `${instance.active_conns}/${effectiveMax}${instance.max_clients <= 0 ? '*' : ''}`
    : `${instance.active_conns}/∞`

  function commit() {
    const n = parseInt(value, 10)
    api.setMaxClients(instance.id, isNaN(n) ? 0 : n).catch(e => alert(String(e)))
    setEditing(false)
  }

  if (!editing) {
    return (
      <span style={{ color: '#6b7280', fontSize: 13, display: 'flex', alignItems: 'center', gap: 6 }}>
        clients: {label}
        {isAdmin && (
          <button onClick={() => setEditing(true)} style={{ background: '#374151', color: '#9ca3af', fontSize: 10, padding: '1px 6px' }}>
            변경
          </button>
        )}
      </span>
    )
  }

  return (
    <span style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 13 }}>
      max:
      <input
        autoFocus
        type="number"
        value={value}
        onChange={e => setValue(e.target.value)}
        onKeyDown={e => { if (e.key === 'Enter') commit(); if (e.key === 'Escape') setEditing(false) }}
        placeholder="∞"
        style={{ width: 55, background: '#111827', color: '#e2e8f0', border: '1px solid #4f46e5', borderRadius: 4, padding: '1px 6px', fontSize: 12 }}
      />
      <button onClick={commit} style={{ background: '#4f46e5', color: '#fff', fontSize: 10, padding: '1px 6px' }}>확인</button>
      <button onClick={() => setEditing(false)} style={{ background: '#374151', color: '#9ca3af', fontSize: 10, padding: '1px 6px' }}>취소</button>
      <span style={{ color: '#4b5563', fontSize: 10 }}>0 이하 = 무제한</span>
    </span>
  )
}

function TimeoutCell({
  seconds, globalTimeout, editable, remaining, onSave,
}: { seconds: number; globalTimeout: number; editable: boolean; remaining: number; onSave: (secs: number) => void }) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(seconds > 0 ? String(seconds) : '')

  useEffect(() => { if (!editing) setValue(seconds > 0 ? String(seconds) : '') }, [seconds, editing])

  function commit() {
    const n = parseInt(value, 10)
    onSave(isNaN(n) ? 0 : n)
    setEditing(false)
  }

  const effectiveLimit = seconds > 0 ? seconds : globalTimeout
  const limitLabel = seconds > 0 ? `${seconds}s` : globalTimeout > 0 ? `${globalTimeout}s*` : '∞'
  const hasLimit = effectiveLimit > 0

  let remainColor = '#fbbf24'
  let remainLabel = ''
  if (hasLimit) {
    if (remaining < 0) {
      remainLabel = 'expired'
      remainColor = '#ef4444'
    } else if (remaining < 30) {
      remainLabel = `${remaining}s`
      remainColor = '#ef4444'
    } else {
      remainLabel = `${remaining}s`
      remainColor = '#fbbf24'
    }
  }

  if (!editing) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <div style={{ display: 'flex', flexDirection: 'column', lineHeight: 1.3 }}>
          {hasLimit ? (
            <>
              <span style={{ color: remainColor, fontSize: 12, fontWeight: 600 }}>{remainLabel}</span>
              <span style={{ color: '#4b5563', fontSize: 10 }}>/ {limitLabel}</span>
            </>
          ) : (
            <span style={{ color: '#6b7280', fontSize: 12 }}>∞</span>
          )}
        </div>
        {editable && (
          <button
            onClick={() => setEditing(true)}
            style={{ background: '#374151', color: '#9ca3af', fontSize: 10, padding: '1px 6px' }}
          >
            변경
          </button>
        )}
      </div>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
        <input
          autoFocus
          type="number"
          value={value}
          onChange={e => setValue(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter') commit()
            if (e.key === 'Escape') { setValue(seconds > 0 ? String(seconds) : ''); setEditing(false) }
          }}
          placeholder="∞"
          style={{ width: 60, background: '#111827', color: '#e2e8f0', border: '1px solid #4f46e5', borderRadius: 4, padding: '1px 6px', fontSize: 12 }}
        />
        <button onClick={commit} style={{ background: '#4f46e5', color: '#fff', fontSize: 10, padding: '1px 6px' }}>확인</button>
        <button onClick={() => { setValue(seconds > 0 ? String(seconds) : ''); setEditing(false) }} style={{ background: '#374151', color: '#9ca3af', fontSize: 10, padding: '1px 6px' }}>취소</button>
      </div>
      <span style={{ color: '#4b5563', fontSize: 10 }}>초 단위 · 0 이하 = 무한</span>
    </div>
  )
}

function HostnameCell({ hostname, onSave }: { hostname: string; onSave: (name: string) => void }) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(hostname)

  // Sync when parent updates hostname from polling
  useEffect(() => { if (!editing) setValue(hostname) }, [hostname, editing])

  if (!editing) {
    return (
      <span
        onClick={() => setEditing(true)}
        title="Click to edit"
        className="hostname-cell"
        style={{ cursor: 'pointer', color: '#a5b4fc', fontWeight: 500 }}
      >
        {value || <span style={{ color: '#4b5563', fontStyle: 'italic' }}>unnamed</span>}
      </span>
    )
  }

  return (
    <input
      autoFocus
      value={value}
      onChange={e => setValue(e.target.value)}
      onBlur={() => { onSave(value); setEditing(false) }}
      onKeyDown={e => {
        if (e.key === 'Enter') { onSave(value); setEditing(false) }
        if (e.key === 'Escape') { setValue(hostname); setEditing(false) }
      }}
      style={{ width: 110, background: '#111827', color: '#e2e8f0', border: '1px solid #4f46e5', borderRadius: 4, padding: '1px 6px', fontSize: 12 }}
    />
  )
}

function statusBadge(status: string): React.CSSProperties {
  const color = status === 'running' ? '#22c55e' : '#f59e0b'
  return {
    display: 'inline-block',
    background: color + '22',
    color,
    borderRadius: 4,
    padding: '1px 8px',
    fontSize: 12,
    fontWeight: 600,
    border: `1px solid ${color}44`,
  }
}

const styles: Record<string, React.CSSProperties> = {
  card: {
    background: '#1a1d2e',
    border: '1px solid #2d3148',
    borderRadius: 10,
    padding: 20,
    display: 'flex',
    flexDirection: 'column',
    gap: 16,
  },
  header: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'flex-start',
    flexWrap: 'wrap',
    gap: 12,
  },
  name: { fontSize: 18, fontWeight: 600, marginBottom: 6 },
  meta: { display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' },
  dim: { color: '#6b7280', fontSize: 13 },
  actions: { display: 'flex', gap: 8, flexWrap: 'wrap' },
  section: {},
  sectionTitle: { fontSize: 13, fontWeight: 600, color: '#9ca3af', marginBottom: 8 },
  empty: { color: '#4b5563', fontSize: 13, fontStyle: 'italic' },
  table: { width: '100%', borderCollapse: 'collapse', fontSize: 13 },
  th: {
    textAlign: 'left',
    padding: '6px 10px',
    background: '#111827',
    color: '#6b7280',
    fontWeight: 500,
    borderBottom: '1px solid #1f2937',
  },
  td: {
    padding: '7px 10px',
    borderBottom: '1px solid #1f2937',
    color: '#d1d5db',
  },
  code: {
    display: 'block',
    background: '#111827',
    borderRadius: 6,
    padding: '8px 12px',
    fontSize: 12,
    color: '#818cf8',
    wordBreak: 'break-all',
  },
  cmdLabel: {
    display: 'flex',
    alignItems: 'center',
    gap: 10,
    marginBottom: 4,
  },
  cmdTag: {
    fontSize: 11,
    fontWeight: 600,
    color: '#9ca3af',
    background: '#1f2937',
    borderRadius: 4,
    padding: '1px 7px',
  },
  copyBtn: {
    color: '#e2e8f0',
    fontSize: 12,
    padding: '2px 10px',
  },
}
