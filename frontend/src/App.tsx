import { useState, useEffect, useCallback, useRef } from 'react'
import { api, Instance } from './api/client'
import InstanceCard from './components/InstanceCard'

const PLATFORMS = [
  { label: 'Linux x64',   file: (n: string) => `${n}-linux-amd64`   },
  { label: 'Linux arm64', file: (n: string) => `${n}-linux-arm64`   },
  { label: 'macOS x64',   file: (n: string) => `${n}-darwin-amd64`  },
  { label: 'macOS arm64', file: (n: string) => `${n}-darwin-arm64`  },
  { label: 'Windows x64', file: (n: string) => `${n}-windows-amd64.exe` },
]

function DownloadMenu({ name }: { name: string }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function close(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])

  return (
    <div ref={ref} style={{ position: 'relative' }}>
      <button onClick={() => setOpen(o => !o)} style={{ background: '#1f2937', color: '#e2e8f0', fontSize: 12 }}>
        {name} ▾
      </button>
      {open && (
        <div style={dlMenuStyle}>
          {PLATFORMS.map(p => (
            <a key={p.label} href={`/download/${p.file(name)}`} download style={dlItemStyle}
               className="dl-item" onClick={() => setOpen(false)}>
              {p.label}
            </a>
          ))}
        </div>
      )}
    </div>
  )
}

const dlMenuStyle: React.CSSProperties = {
  position: 'absolute', top: '100%', right: 0, marginTop: 4, zIndex: 100,
  background: '#1f2937', border: '1px solid #374151', borderRadius: 6,
  display: 'flex', flexDirection: 'column', minWidth: 140, boxShadow: '0 4px 12px #0006',
}
const dlItemStyle: React.CSSProperties = {
  padding: '7px 14px', color: '#d1d5db', fontSize: 12, textDecoration: 'none',
  whiteSpace: 'nowrap', cursor: 'pointer',
}

export default function App() {
  const [instances, setInstances] = useState<Instance[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)

  const load = useCallback(async () => {
    try {
      const data = await api.listInstances()
      data.sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime())
      setInstances(data)
      setError(null)
    } catch (e) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(load, 10_000)
    return () => clearInterval(timer)
  }, [load])

  async function create(e: React.FormEvent) {
    e.preventDefault()
    if (!newName.trim()) return
    setCreating(true)
    try {
      const inst = await api.createInstance(newName.trim())
      setInstances(prev =>
        [...prev, inst].sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime())
      )
      setNewName('')
    } catch (e) {
      alert(String(e))
    } finally {
      setCreating(false)
    }
  }

  function handleDelete(id: string) {
    setInstances(prev => prev.filter(i => i.id !== id))
  }

  return (
    <div style={styles.root}>
      <header style={styles.header}>
        <div style={styles.title}>
          <span style={styles.logo}>⬡</span> OTIV
        </div>
        <div style={styles.headerRight}>
          <form onSubmit={create} style={styles.form}>
            <input
              value={newName}
              onChange={e => setNewName(e.target.value)}
              placeholder="Instance name"
              style={{ width: 180 }}
              disabled={creating}
            />
            <button
              type="submit"
              disabled={creating || !newName.trim()}
              style={{ background: '#6366f1', color: '#fff' }}
            >
              {creating ? 'Creating...' : '+ New Instance'}
            </button>
          </form>
          <div style={styles.dlGroup}>
            <span style={styles.dlLabel}>Download:</span>
            <DownloadMenu name="otiv-client" />
          </div>
        </div>
      </header>

      <main style={styles.main}>
        {loading && <div style={styles.center}>Loading...</div>}
        {error && <div style={styles.error}>{error}</div>}
        {!loading && instances.length === 0 && (
          <div style={styles.center}>No VPN instances. Create one above.</div>
        )}
        <div style={styles.grid}>
          {instances.map(inst => (
            <InstanceCard key={inst.id} instance={inst} onDelete={handleDelete} />
          ))}
        </div>
      </main>
    </div>
  )
}

const styles: Record<string, React.CSSProperties> = {
  root: { minHeight: '100vh', display: 'flex', flexDirection: 'column' },
  header: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    padding: '16px 24px',
    background: '#13151f',
    borderBottom: '1px solid #1f2937',
    flexWrap: 'wrap',
    gap: 12,
  },
  title: { fontSize: 20, fontWeight: 700, display: 'flex', alignItems: 'center', gap: 8 },
  logo: { color: '#6366f1', fontSize: 22 },
  headerRight: { display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' },
  form: { display: 'flex', gap: 8 },
  dlGroup: { display: 'flex', alignItems: 'center', gap: 6 },
  dlLabel: { fontSize: 12, color: '#6b7280' },
  main: { flex: 1, padding: 24 },
  grid: { display: 'flex', flexDirection: 'column', gap: 16 },
  center: { textAlign: 'center', color: '#6b7280', padding: 40 },
  error: {
    background: '#dc262622',
    border: '1px solid #dc2626',
    borderRadius: 8,
    padding: '12px 16px',
    color: '#fca5a5',
    marginBottom: 16,
  },
}
