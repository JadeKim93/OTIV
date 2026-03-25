import { useState } from 'react'
import { Instance, VPNClient, api, fmtBytes } from '../api/client'

interface Props {
  instance: Instance
  onDelete: (id: string) => void
}

export default function InstanceCard({ instance, onDelete }: Props) {
  const [clients, setClients] = useState<VPNClient[]>(instance.clients)
  const [refreshing, setRefreshing] = useState(false)
  const [deleting, setDeleting] = useState(false)

  async function refresh() {
    setRefreshing(true)
    try {
      setClients(await api.getClients(instance.id))
    } catch {
      // management interface might not be ready yet
    } finally {
      setRefreshing(false)
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

  const createdAt = new Date(instance.created_at).toLocaleString()

  return (
    <div style={styles.card}>
      <div style={styles.header}>
        <div>
          <div style={styles.name}>{instance.name}</div>
          <div style={styles.meta}>
            <span style={statusBadge(instance.status)}>{instance.status}</span>
            <span style={styles.dim}>subnet: {instance.subnet}/24</span>
            <span style={styles.dim}>created: {createdAt}</span>
          </div>
        </div>
        <div style={styles.actions}>
          <button
            onClick={refresh}
            disabled={refreshing}
            style={{ background: '#374151', color: '#e2e8f0' }}
          >
            {refreshing ? '...' : 'Refresh'}
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
          <div style={styles.empty}>No clients connected</div>
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
        <div style={styles.sectionTitle}>Instance ID</div>
        <code style={styles.code}>{instance.id}</code>
        <div style={styles.dim} hidden>WSS path: /vpn/{instance.id}</div>
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
}
