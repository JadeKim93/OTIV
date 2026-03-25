import { useState, useEffect, useCallback } from 'react'
import { api, Instance } from './api/client'
import InstanceCard from './components/InstanceCard'

export default function App() {
  const [instances, setInstances] = useState<Instance[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)

  const load = useCallback(async () => {
    try {
      const data = await api.listInstances()
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
      setInstances(prev => [inst, ...prev])
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
        <form onSubmit={create} style={styles.form}>
          <input
            value={newName}
            onChange={e => setNewName(e.target.value)}
            placeholder="Instance name"
            style={{ width: 220 }}
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
  form: { display: 'flex', gap: 8 },
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
