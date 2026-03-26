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
  if (navigator.clipboard) {
    navigator.clipboard.writeText(text).catch(() => fallbackCopy(text))
  } else {
    fallbackCopy(text)
  }
}

function fallbackCopy(text: string) {
  const el = document.createElement('textarea')
  el.value = text
  el.style.cssText = 'position:fixed;opacity:0;top:0;left:0'
  document.body.appendChild(el)
  el.focus()
  el.select()
  document.execCommand('copy')
  document.body.removeChild(el)
}

interface Props {
  instance: Instance
  onDelete: (id: string) => void
}

export default function InstanceCard({ instance, onDelete }: Props) {
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
                {['Hostname', 'Common Name', 'Virtual IP', 'Sent', 'Received', 'Connected Since', ''].map(h => (
                  <th key={h} style={styles.th}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {clients.map((c, i) => (
                <tr key={i} className="client-row">
                  <td style={styles.td}>
                    <HostnameCell
                      hostname={c.hostname ?? ''}
                      onSave={name => api.setHostname(instance.id, c.common_name, name).catch(e => alert(String(e)))}
                    />
                  </td>
                  <td style={{ ...styles.td, color: '#6b7280', fontSize: 11 }}>{c.common_name}</td>
                  <td style={styles.td}>{c.virtual_ip}</td>
                  <td style={styles.td}>{fmtBytes(c.bytes_sent)}</td>
                  <td style={styles.td}>{fmtBytes(c.bytes_recv)}</td>
                  <td style={styles.td}>
                    {c.connected_at ? new Date(c.connected_at).toLocaleString() : '-'}
                  </td>
                  <td style={styles.td}>
                    <button
                      onClick={() => api.kickClient(instance.id, c.common_name).catch(e => alert(String(e)))}
                      style={{ background: '#7c3aed', color: '#fff', fontSize: 11, padding: '2px 8px' }}
                    >
                      Kick
                    </button>
                  </td>
                </tr>
              ))}
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
