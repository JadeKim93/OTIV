import { useState, useEffect, useCallback, useRef } from 'react'
import { api, auth, Instance, BlockedEntry } from './api/client'
import InstanceCard from './components/InstanceCard'

const PLATFORMS = [
  { label: 'Linux x64',   file: (n: string) => `${n}-linux-amd64`,       dl: 'otiv-client'      },
  { label: 'Linux arm64', file: (n: string) => `${n}-linux-arm64`,       dl: 'otiv-client'      },
  { label: 'macOS x64',   file: (n: string) => `${n}-darwin-amd64`,      dl: 'otiv-client'      },
  { label: 'macOS arm64', file: (n: string) => `${n}-darwin-arm64`,      dl: 'otiv-client'      },
  { label: 'Windows x64', file: (n: string) => `${n}-windows-amd64.exe`, dl: 'otiv-client.exe'  },
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
            <a key={p.label} href={`/download/${p.file(name)}`} download={p.dl} style={dlItemStyle}
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

// ── 로그인 페이지 ──────────────────────────────────────────────────────────────
function LoginPage({ onLogin }: { onLogin: (role: string) => void }) {
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!password) return
    setLoading(true)
    setError(null)
    try {
      const res = await api.login(password)
      auth.save(res.token, res.role)
      onLogin(res.role)
    } catch {
      setError('비밀번호가 올바르지 않습니다.')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={loginStyles.overlay}>
      <div style={loginStyles.box}>
        <div style={loginStyles.logo}>⬡</div>
        <div style={loginStyles.title}>OTIV</div>
        <div style={loginStyles.subtitle}>접속 비밀번호를 입력하세요</div>
        <form onSubmit={handleSubmit} style={loginStyles.form}>
          <input
            type="password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            placeholder="비밀번호"
            autoFocus
            style={loginStyles.input}
          />
          <button
            type="submit"
            disabled={loading || !password}
            style={loginStyles.button}
          >
            {loading ? '확인 중...' : '접속'}
          </button>
        </form>
        {error && <div style={loginStyles.error}>{error}</div>}
      </div>
    </div>
  )
}

// ── 관리자 비밀번호 팝업 ──────────────────────────────────────────────────────
function AdminPasswordModal({ onClose, onSuccess }: { onClose: () => void; onSuccess: () => void }) {
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!password) return
    setLoading(true)
    setError(null)
    try {
      const res = await api.login(password)
      if (res.role !== 'admin') {
        setError('관리자 비밀번호가 올바르지 않습니다.')
        return
      }
      auth.save(res.token, res.role)
      onSuccess()
    } catch {
      setError('관리자 비밀번호가 올바르지 않습니다.')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={modalStyles.overlay} onClick={onClose}>
      <div style={modalStyles.box} onClick={e => e.stopPropagation()}>
        <div style={modalStyles.title}>관리자 모드</div>
        <div style={modalStyles.subtitle}>관리자 비밀번호를 입력하세요</div>
        <form onSubmit={handleSubmit} style={loginStyles.form}>
          <input
            type="password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            placeholder="관리자 비밀번호"
            autoFocus
            style={loginStyles.input}
          />
          <button
            type="submit"
            disabled={loading || !password}
            style={{ ...loginStyles.button, background: '#7c3aed' }}
          >
            {loading ? '확인 중...' : '관리자 접속'}
          </button>
        </form>
        {error && <div style={loginStyles.error}>{error}</div>}
        <button onClick={onClose} style={modalStyles.cancelBtn}>취소</button>
      </div>
    </div>
  )
}

// ── 차단 목록 패널 ────────────────────────────────────────────────────────
function BlockedPanel() {
  const [entries, setEntries] = useState<BlockedEntry[]>([])
  const [newIP, setNewIP] = useState('')
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    try { setEntries(await api.listBlocked()) } catch {}
  }, [])

  useEffect(() => { load() }, [load])

  async function handleBlock(e: React.FormEvent) {
    e.preventDefault()
    if (!newIP.trim()) return
    setLoading(true)
    try {
      await api.blockIP(newIP.trim())
      setNewIP('')
      await load()
    } catch (e) { alert(String(e)) }
    finally { setLoading(false) }
  }

  async function handleUnblock(ip: string) {
    try {
      await api.unblockIP(ip)
      setEntries(prev => prev.filter(e => e.ip !== ip))
    } catch (e) { alert(String(e)) }
  }

  return (
    <div style={blockedStyles.panel}>
      <div style={blockedStyles.title}>차단된 IP</div>
      <form onSubmit={handleBlock} style={blockedStyles.form}>
        <input
          value={newIP}
          onChange={e => setNewIP(e.target.value)}
          placeholder="IP 주소"
          style={blockedStyles.input}
          disabled={loading}
        />
        <button type="submit" disabled={loading || !newIP.trim()} style={blockedStyles.addBtn}>
          차단
        </button>
      </form>
      <div style={blockedStyles.list}>
        {entries.length === 0
          ? <div style={{ color: '#4b5563', fontSize: 12, fontStyle: 'italic' }}>차단된 IP 없음</div>
          : entries.map(e => (
            <div key={e.ip} style={blockedStyles.entry}>
              <div>
                <div style={{ fontWeight: 600, fontSize: 13 }}>{e.ip}</div>
                <div style={{ fontSize: 11, color: '#6b7280' }}>
                  {new Date(e.blocked_at).toLocaleString()}
                </div>
              </div>
              <button onClick={() => handleUnblock(e.ip)} style={blockedStyles.unblockBtn}>
                해제
              </button>
            </div>
          ))
        }
      </div>
    </div>
  )
}

const blockedStyles: Record<string, React.CSSProperties> = {
  panel: {
    position: 'absolute', top: '100%', right: 0, marginTop: 8, zIndex: 200,
    background: '#13151f', border: '1px solid #374151', borderRadius: 8,
    padding: 14, width: 280, boxShadow: '0 8px 24px #0008',
  },
  title: { fontSize: 13, fontWeight: 700, color: '#9ca3af', marginBottom: 10 },
  form: { display: 'flex', gap: 6, marginBottom: 10 },
  input: {
    flex: 1, background: '#1f2937', border: '1px solid #374151', borderRadius: 4,
    padding: '4px 8px', color: '#e2e8f0', fontSize: 12,
  },
  addBtn: { background: '#dc2626', color: '#fff', fontSize: 12, padding: '4px 10px', borderRadius: 4 },
  list: { display: 'flex', flexDirection: 'column', gap: 6, maxHeight: 240, overflowY: 'auto' },
  entry: {
    display: 'flex', justifyContent: 'space-between', alignItems: 'center',
    background: '#1f2937', borderRadius: 6, padding: '6px 10px',
  },
  unblockBtn: { background: '#374151', color: '#9ca3af', fontSize: 11, padding: '2px 8px', borderRadius: 4 },
}

// ── 메인 앱 ───────────────────────────────────────────────────────────────────
export default function App() {
  const [authenticated, setAuthenticated] = useState(auth.isAuthenticated())
  const [isAdmin, setIsAdmin] = useState(auth.isAdmin())
  const [showAdminModal, setShowAdminModal] = useState(false)
  const [showBlocked, setShowBlocked] = useState(false)
  const blockedRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!showBlocked) return
    function close(e: MouseEvent) {
      if (blockedRef.current && !blockedRef.current.contains(e.target as Node)) setShowBlocked(false)
    }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [showBlocked])
  const [instances, setInstances] = useState<Instance[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)

  // 로고 5번 클릭 감지 (1000ms 이내)
  const clickTimesRef = useRef<number[]>([])
  function handleLogoClick() {
    const now = Date.now()
    clickTimesRef.current = [...clickTimesRef.current, now].filter(t => now - t <= 1000)
    if (clickTimesRef.current.length >= 5) {
      clickTimesRef.current = []
      if (!isAdmin) setShowAdminModal(true)
    }
  }

  const load = useCallback(async () => {
    try {
      const data = await api.listInstances()
      data.sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime())
      setInstances(data)
      setError(null)
    } catch (e) {
      const msg = String(e)
      // 인증 만료 시 로그인 페이지로
      if (msg.includes('unauthorized') || msg.includes('401')) {
        auth.clear()
        setAuthenticated(false)
        setIsAdmin(false)
      } else {
        setError(msg)
      }
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!authenticated) return
    load()
    const timer = setInterval(load, 10_000)
    return () => clearInterval(timer)
  }, [load, authenticated])

  function handleLogin(role: string) {
    setAuthenticated(true)
    setIsAdmin(role === 'admin')
  }

  function handleAdminSuccess() {
    setIsAdmin(true)
    setShowAdminModal(false)
  }

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

  if (!authenticated) {
    return <LoginPage onLogin={handleLogin} />
  }

  return (
    <div style={styles.root}>
      {showAdminModal && (
        <AdminPasswordModal
          onClose={() => setShowAdminModal(false)}
          onSuccess={handleAdminSuccess}
        />
      )}

      <header style={styles.header}>
        <div style={styles.title}>
          <span
            style={{ ...styles.logo, cursor: 'pointer' }}
            onClick={handleLogoClick}
            title="OTIV"
          >
            ⬡
          </span>
          OTIV
          {isAdmin && (
            <span style={styles.adminBadge}>관리자</span>
          )}
        </div>
        <div style={styles.headerRight}>
          {isAdmin && (
            <div ref={blockedRef} style={{ position: 'relative' }}>
              <button
                onClick={() => setShowBlocked(o => !o)}
                style={{ background: '#dc262622', color: '#fca5a5', border: '1px solid #dc262644', fontSize: 12 }}
              >
                차단 목록
              </button>
              {showBlocked && <BlockedPanel />}
            </div>
          )}
          {isAdmin && (
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
          )}
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
          <div style={styles.center}>No VPN instances.{isAdmin ? ' Create one above.' : ''}</div>
        )}
        <div style={styles.grid}>
          {instances.map(inst => (
            <InstanceCard key={inst.id} instance={inst} onDelete={handleDelete} isAdmin={isAdmin} />
          ))}
        </div>
      </main>
    </div>
  )
}

const loginStyles: Record<string, React.CSSProperties> = {
  overlay: {
    minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center',
    background: '#0d0f1a',
  },
  box: {
    background: '#13151f', border: '1px solid #1f2937', borderRadius: 12,
    padding: '40px 36px', width: 320, display: 'flex', flexDirection: 'column',
    alignItems: 'center', gap: 12, boxShadow: '0 8px 32px #0008',
  },
  logo: { color: '#6366f1', fontSize: 40 },
  title: { fontSize: 24, fontWeight: 700, color: '#e2e8f0' },
  subtitle: { fontSize: 13, color: '#6b7280', marginBottom: 8 },
  form: { display: 'flex', flexDirection: 'column', gap: 10, width: '100%' },
  input: {
    background: '#1f2937', border: '1px solid #374151', borderRadius: 6,
    padding: '10px 14px', color: '#e2e8f0', fontSize: 14, width: '100%',
    boxSizing: 'border-box' as const,
  },
  button: {
    background: '#6366f1', color: '#fff', border: 'none', borderRadius: 6,
    padding: '10px 0', fontSize: 14, fontWeight: 600, cursor: 'pointer', width: '100%',
  },
  error: { color: '#fca5a5', fontSize: 13, marginTop: 4 },
}

const modalStyles: Record<string, React.CSSProperties> = {
  overlay: {
    position: 'fixed', inset: 0, background: '#0009', display: 'flex',
    alignItems: 'center', justifyContent: 'center', zIndex: 200,
  },
  box: {
    background: '#13151f', border: '1px solid #4c1d95', borderRadius: 12,
    padding: '32px 28px', width: 300, display: 'flex', flexDirection: 'column',
    alignItems: 'center', gap: 10, boxShadow: '0 8px 32px #0008',
  },
  title: { fontSize: 18, fontWeight: 700, color: '#a78bfa' },
  subtitle: { fontSize: 13, color: '#6b7280', marginBottom: 4 },
  cancelBtn: {
    background: 'transparent', color: '#6b7280', border: 'none',
    fontSize: 13, cursor: 'pointer', marginTop: 4,
  },
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
  adminBadge: {
    fontSize: 11, fontWeight: 600, background: '#4c1d9533', color: '#a78bfa',
    border: '1px solid #4c1d9555', borderRadius: 4, padding: '1px 8px',
  },
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
