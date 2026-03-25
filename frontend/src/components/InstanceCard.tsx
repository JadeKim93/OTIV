import { useState, useEffect, useRef } from 'react'
import { Instance, VPNClient, api, fmtBytes } from '../api/client'

function cliCommand(instanceId: string): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `sudo otiv-client -url ${proto}://${location.host}/vpn/${instanceId}`
}

function proxyCommand(instanceId: string): string {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `otiv-proxy -url ${proto}://${location.host}/vpn/${instanceId}`
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
  const [copiedClient, setCopiedClient] = useState(false)
  const [copiedProxy, setCopiedProxy] = useState(false)
  const pollingRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // Sync status when parent re-renders (10s poll)
  useEffect(() => { setStatus(instance.status) }, [instance.status])

  // Poll clients every 200ms when running
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

  function copyClient() {
    navigator.clipboard.writeText(cliCommand(instance.id))
    setCopiedClient(true)
    setTimeout(() => setCopiedClient(false), 2000)
  }

  function copyProxy() {
    navigator.clipboard.writeText(proxyCommand(instance.id))
    setCopiedProxy(true)
    setTimeout(() => setCopiedProxy(false), 2000)
  }

  const createdAt = new Date(instance.created_at).toLocaleString()
  const isRunning = status === 'running'

  return (
    <div style={styles.card}>
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
          <a href={api.clientConfigUrl(instance.id)} download>
            <button style={{ background: '#4f46e5', color: '#fff' }}>
              Download .ovpn
            </button>
          </a>
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
                {['Common Name', 'Real Address', 'Virtual IP', 'Sent', 'Received', 'Connected Since'].map(h => (
                  <th key={h} style={styles.th}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {clients.map((c, i) => (
                <tr key={i}>
                  <td style={styles.td}>{c.common_name}</td>
                  <td style={styles.td}>{c.real_addr}</td>
                  <td style={styles.td}>{c.virtual_ip}</td>
                  <td style={styles.td}>{fmtBytes(c.bytes_sent)}</td>
                  <td style={styles.td}>{fmtBytes(c.bytes_recv)}</td>
                  <td style={styles.td}>
                    {c.connected_at ? new Date(c.connected_at).toLocaleString() : '-'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div style={styles.section}>
        <div style={styles.sectionTitle}>Connect</div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div>
            <div style={styles.cmdLabel}>
              <span style={styles.cmdTag}>otiv-client</span>
              <button onClick={copyClient} style={{ ...styles.copyBtn, background: copiedClient ? '#16a34a' : '#374151' }}>
                {copiedClient ? 'Copied!' : 'Copy'}
              </button>
            </div>
            <code style={styles.code}>{cliCommand(instance.id)}</code>
          </div>
          <div>
            <div style={styles.cmdLabel}>
              <span style={styles.cmdTag}>otiv-proxy</span>
              <button onClick={copyProxy} style={{ ...styles.copyBtn, background: copiedProxy ? '#16a34a' : '#374151' }}>
                {copiedProxy ? 'Copied!' : 'Copy'}
              </button>
            </div>
            <code style={styles.code}>{proxyCommand(instance.id)}</code>
          </div>
        </div>
      </div>
    </div>
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
