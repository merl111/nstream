import { useState } from 'react'
import { Routes, Route, NavLink, Navigate, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type Container, type User, type Job, type TMDBSearchResult, type Language } from '../api/client'

// ---- Containers tab -------------------------------------------------------

function AdminContainers() {
  useQueryClient()
  const { data: containers = [], isLoading, refetch } = useQuery({
    queryKey: ['containers'],
    queryFn: api.listContainers,
  })

  const [mode, setMode] = useState<'add' | 'create'>('add')

  // "Add existing" form state
  const [cid, setCid] = useState('')
  const [addName, setAddName] = useState('')

  // "Create new" form state
  const [createName, setCreateName] = useState('')
  const [replicas, setReplicas] = useState(2)
  const [publicRead, setPublicRead] = useState(false)

  const [err, setErr] = useState('')
  const [ok, setOk] = useState('')
  const [scanningId, setScanningId] = useState<number | null>(null)

  function flash(msg: string) {
    setOk(msg)
    setTimeout(() => setOk(''), 4000)
  }

  const add = useMutation({
    mutationFn: () => api.addContainer(cid, addName || cid),
    onSuccess: (c) => {
      setCid('')
      setAddName('')
      setErr('')
      flash(`Container "${c.name}" added.`)
      refetch()
    },
    onError: (e: unknown) => setErr(e instanceof Error ? e.message : 'Error'),
  })

  const create = useMutation({
    mutationFn: () => api.createNeoFSContainer(createName, replicas, publicRead),
    onSuccess: (c) => {
      setCreateName('')
      setErr('')
      flash(`Container "${c.name}" created (${c.cid}).`)
      refetch()
    },
    onError: (e: unknown) => setErr(e instanceof Error ? e.message : 'Error'),
  })

  const del = useMutation({
    mutationFn: (id: number) => api.deleteContainer(id),
    onSuccess: () => refetch(),
    onError: (e: unknown) => setErr(e instanceof Error ? e.message : 'Error'),
  })

  const scan = useMutation({
    mutationFn: async (id: number) => { setScanningId(id); await api.scanContainer(id) },
    onSettled: () => setScanningId(null),
  })

  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Containers</h2>

      {/* Mode toggle */}
      <div className="flex gap-1 mb-4 p-1 bg-gray-900 rounded-lg w-fit">
        <button
          onClick={() => { setMode('add'); setErr(''); setOk('') }}
          className={`px-4 py-1.5 text-sm rounded-md transition-colors ${mode === 'add' ? 'bg-gray-700 text-white' : 'text-gray-400 hover:text-white'}`}
        >
          Add existing
        </button>
        <button
          onClick={() => { setMode('create'); setErr(''); setOk('') }}
          className={`px-4 py-1.5 text-sm rounded-md transition-colors ${mode === 'create' ? 'bg-gray-700 text-white' : 'text-gray-400 hover:text-white'}`}
        >
          Create new
        </button>
      </div>

      {/* Add existing */}
      {mode === 'add' && (
        <form onSubmit={e => { e.preventDefault(); setErr(''); setOk(''); add.mutate() }} className="flex flex-col sm:flex-row gap-2 mb-4">
          <input
            placeholder="NeoFS Container ID"
            value={cid}
            onChange={e => setCid(e.target.value)}
            required
            className="flex-1 px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-white text-sm focus:outline-none focus:border-indigo-500"
          />
          <input
            placeholder="Label (optional)"
            value={addName}
            onChange={e => setAddName(e.target.value)}
            className="flex-1 px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-white text-sm focus:outline-none focus:border-indigo-500"
          />
          <button type="submit" disabled={add.isPending}
            className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 rounded-lg text-sm font-medium disabled:opacity-50 transition-colors whitespace-nowrap">
            {add.isPending ? 'Adding…' : 'Add'}
          </button>
        </form>
      )}

      {/* Create new */}
      {mode === 'create' && (
        <form onSubmit={e => { e.preventDefault(); setErr(''); setOk(''); create.mutate() }} className="space-y-3 mb-4">
          <div className="flex flex-col sm:flex-row gap-2">
            <input
              placeholder="Container name"
              value={createName}
              onChange={e => setCreateName(e.target.value)}
              required
              className="flex-1 px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-white text-sm focus:outline-none focus:border-indigo-500"
            />
            <div className="flex items-center gap-2 shrink-0">
              <label className="text-sm text-gray-400 whitespace-nowrap">Replicas</label>
              <select
                value={replicas}
                onChange={e => setReplicas(Number(e.target.value))}
                className="px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-white text-sm focus:outline-none"
              >
                {[1, 2, 3, 4, 5].map(n => <option key={n} value={n}>{n}</option>)}
              </select>
            </div>
          </div>
          <div className="flex items-center gap-3 flex-wrap">
            <label className="flex items-center gap-2 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={publicRead}
                onChange={e => setPublicRead(e.target.checked)}
                className="w-4 h-4 accent-indigo-500"
              />
              <span className="text-sm text-gray-300">Public read</span>
            </label>
            <button type="submit" disabled={create.isPending}
              className="ml-auto px-4 py-2 bg-indigo-600 hover:bg-indigo-500 rounded-lg text-sm font-medium disabled:opacity-50 transition-colors whitespace-nowrap">
              {create.isPending ? 'Creating on NeoFS…' : 'Create container'}
            </button>
          </div>
          {create.isPending && (
            <p className="text-xs text-indigo-400 animate-pulse">
              Sending transaction to NeoFS and waiting for propagation (up to 30 s)…
            </p>
          )}
          {!create.isPending && (
            <p className="text-xs text-gray-500">
              Creates a new container on NeoFS. May take up to 30 s to propagate.
            </p>
          )}
        </form>
      )}

      {/* Feedback */}
      {err && (
        <div className="mb-4 px-3 py-2 bg-red-900/40 border border-red-700 rounded-lg text-red-300 text-sm">
          {err}
        </div>
      )}
      {ok && (
        <div className="mb-4 px-3 py-2 bg-green-900/40 border border-green-700 rounded-lg text-green-300 text-sm">
          ✓ {ok}
        </div>
      )}

      {/* List */}
      {isLoading && <p className="text-gray-500">Loading…</p>}
      <div className="space-y-2">
        {containers.map((c: Container) => (
          <div key={c.id} className="flex items-center gap-3 p-3 bg-gray-900 rounded-lg">
            <div className="flex-1 min-w-0">
              <p className="font-medium text-white">{c.name}</p>
              <p className="text-xs font-mono text-gray-500 truncate">{c.cid}</p>
              {c.last_scanned_at
                ? <p className="text-xs text-gray-600">Last scan: {new Date(c.last_scanned_at).toLocaleString()}</p>
                : <p className="text-xs text-gray-700">Never scanned</p>
              }
            </div>
            <button
              onClick={() => scan.mutate(c.id)}
              disabled={scanningId === c.id}
              className="px-3 py-1 text-xs bg-gray-800 hover:bg-gray-700 disabled:opacity-50 rounded transition-colors whitespace-nowrap"
            >
              {scanningId === c.id ? 'Scanning…' : 'Scan now'}
            </button>
            <button
              onClick={() => del.mutate(c.id)}
              disabled={del.isPending}
              className="px-3 py-1 text-xs bg-red-900/50 hover:bg-red-800 rounded transition-colors text-red-300"
            >
              Delete
            </button>
          </div>
        ))}
        {containers.length === 0 && !isLoading && (
          <p className="text-gray-500 text-sm">No containers yet.</p>
        )}
      </div>
    </div>
  )
}

// ---- Users tab ------------------------------------------------------------

function AdminUsers() {
  const qc = useQueryClient()
  const { data: users = [], isLoading } = useQuery({
    queryKey: ['users'],
    queryFn: api.listUsers,
  })
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<'viewer' | 'admin'>('viewer')
  const [err, setErr] = useState('')

  const create = useMutation({
    mutationFn: () => api.createUser(username, password, role),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['users'] }); setUsername(''); setPassword('') },
    onError: (e: unknown) => setErr(e instanceof Error ? e.message : 'Error'),
  })
  const del = useMutation({
    mutationFn: (id: number) => api.deleteUser(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })

  return (
    <div>
      <h2 className="text-xl font-semibold mb-4">Users</h2>
      <form onSubmit={e => { e.preventDefault(); setErr(''); create.mutate() }} className="flex flex-col sm:flex-row gap-2 mb-6">
        <input placeholder="Username" value={username} onChange={e => setUsername(e.target.value)} required
          className="flex-1 px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-white text-sm focus:outline-none focus:border-indigo-500" />
        <input placeholder="Password" type="password" value={password} onChange={e => setPassword(e.target.value)} required
          className="flex-1 px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-white text-sm focus:outline-none focus:border-indigo-500" />
        <select value={role} onChange={e => setRole(e.target.value as 'viewer' | 'admin')}
          className="px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-white text-sm focus:outline-none">
          <option value="viewer">Viewer</option>
          <option value="admin">Admin</option>
        </select>
        <button type="submit" disabled={create.isPending}
          className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 rounded-lg text-sm font-medium disabled:opacity-50 transition-colors">
          Create
        </button>
      </form>
      {err && <p className="text-red-400 text-sm mb-4">{err}</p>}

      {isLoading && <p className="text-gray-500">Loading…</p>}
      <div className="space-y-2">
        {users.map((u: User) => (
          <div key={u.id} className="flex items-center gap-3 p-3 bg-gray-900 rounded-lg">
            <div className="flex-1">
              <span className="font-medium text-white">{u.username}</span>
              <span className={`ml-2 text-xs px-2 py-0.5 rounded-full ${u.role === 'admin' ? 'bg-indigo-800 text-indigo-200' : 'bg-gray-800 text-gray-400'}`}>
                {u.role}
              </span>
            </div>
            <button onClick={() => del.mutate(u.id)}
              className="px-3 py-1 text-xs bg-red-900/50 hover:bg-red-800 rounded transition-colors text-red-300">
              Delete
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

// ---- Jobs tab -------------------------------------------------------------

const statusColors: Record<string, string> = {
  pending: 'text-yellow-400 bg-yellow-900/30',
  running: 'text-blue-400 bg-blue-900/30',
  done: 'text-green-400 bg-green-900/30',
  failed: 'text-red-400 bg-red-900/30',
}

function AdminJobs() {
  const { data: jobs = [], isLoading, refetch } = useQuery({
    queryKey: ['jobs'],
    queryFn: api.listJobs,
    refetchInterval: 5000,
  })

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-xl font-semibold">Transcode Jobs</h2>
        <button onClick={() => refetch()} className="text-sm text-gray-400 hover:text-white transition-colors">
          Refresh
        </button>
      </div>
      {isLoading && <p className="text-gray-500">Loading…</p>}
      {jobs.length === 0 && !isLoading && (
        <p className="text-gray-500 text-sm">No jobs yet. Queue one from a video's watch page.</p>
      )}
      <div className="space-y-2">
        {jobs.map((j: Job) => (
          <div key={j.id} className="p-3 bg-gray-900 rounded-lg flex items-start gap-3">
            <span className={`text-xs px-2 py-0.5 rounded-full font-medium mt-0.5 ${statusColors[j.status] ?? ''}`}>
              {j.status}
            </span>
            <div className="flex-1 min-w-0">
              <p className="text-sm text-white">
                Job #{j.id} — Video #{j.video_id} — {j.profile}
              </p>
              {j.error && <p className="text-xs text-red-400 mt-1 truncate">{j.error}</p>}
              <p className="text-xs text-gray-600 mt-0.5">
                Created: {new Date(j.created_at).toLocaleString()}
                {j.finished_at ? ` · Finished: ${new Date(j.finished_at).toLocaleString()}` : ''}
              </p>
            </div>
            <Link to={`/watch/${j.video_id}`} className="text-xs text-indigo-400 hover:text-indigo-300 shrink-0">
              Watch →
            </Link>
          </div>
        ))}
      </div>
    </div>
  )
}

// ---- TMDB Import tab -------------------------------------------------------

const TMDB_POSTER = 'https://image.tmdb.org/t/p/w92'

function LangSelect({ languages, value, onChange }: { languages: Language[]; value: string; onChange: (v: string) => void }) {
  return (
    <select
      value={value}
      onChange={e => onChange(e.target.value)}
      className="px-2.5 py-2 rounded-lg bg-gray-800 border border-white/10 text-sm text-gray-300 focus:outline-none focus:ring-2 focus:ring-indigo-500"
    >
      {languages.map(l => (
        <option key={l.Code} value={l.Code}>{l.English} ({l.Code})</option>
      ))}
    </select>
  )
}

function AdminMedia() {
  const qc = useQueryClient()
  const [q, setQ] = useState('')
  const [searchQ, setSearchQ] = useState('')
  const [mediaType, setMediaType] = useState<'movie' | 'tv'>('movie')
  const [lang, setLang] = useState('en-US')
  const [err, setErr] = useState('')
  const [ok, setOk] = useState('')
  const [importingId, setImportingId] = useState<number | null>(null)

  const { data: languages = [] } = useQuery({
    queryKey: ['languages'],
    queryFn: api.listLanguages,
  })

  const { data: results = [], isFetching } = useQuery({
    queryKey: ['tmdb-search', searchQ, mediaType, lang],
    queryFn: () => api.tmdbSearch(searchQ, mediaType, undefined, lang),
    enabled: searchQ.length > 1,
  })

  const { data: imported } = useQuery({
    queryKey: ['media', 'all'],
    queryFn: () => api.listMedia({ limit: 200 }),
  })
  const importedTmdbIds = new Set((imported?.items ?? []).map(m => m.tmdb_id))

  const importMutation = useMutation({
    mutationFn: ({ r, language }: { r: TMDBSearchResult; language: string }) => {
      setImportingId(r.id)
      return api.importMedia(r.id, r.media_type as 'movie' | 'tv', language)
    },
    onSuccess: (item) => {
      setErr('')
      setImportingId(null)
      setOk(`"${item.title}" imported!`)
      setTimeout(() => setOk(''), 3000)
      qc.invalidateQueries({ queryKey: ['media'] })
    },
    onError: (e: unknown) => {
      setImportingId(null)
      setErr(e instanceof Error ? e.message : 'Import failed')
    },
  })

  return (
    <div className="space-y-6">
      {/* Search / import */}
      <div>
        <h2 className="text-lg font-semibold mb-4">Import from TMDB</h2>
        <div className="flex gap-2 flex-wrap">
          {/* Type toggle */}
          <div className="flex rounded-lg overflow-hidden border border-white/10">
            {(['movie', 'tv'] as const).map(t => (
              <button key={t} onClick={() => setMediaType(t)}
                className={`px-3 py-2 text-sm capitalize transition-colors ${mediaType === t ? 'bg-indigo-600 text-white' : 'bg-gray-800 text-gray-400 hover:text-white'}`}>
                {t === 'tv' ? 'TV Show' : 'Movie'}
              </button>
            ))}
          </div>

          {/* Language */}
          {languages.length > 0 && (
            <LangSelect languages={languages} value={lang} onChange={setLang} />
          )}

          {/* Query */}
          <input
            value={q}
            onChange={e => setQ(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && setSearchQ(q)}
            placeholder={`Search TMDB for a ${mediaType === 'tv' ? 'TV show' : 'movie'}…`}
            className="flex-1 min-w-[200px] px-3 py-2 rounded-lg bg-gray-800 border border-white/10 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
          <button onClick={() => setSearchQ(q)}
            className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 rounded-lg text-sm transition-colors">
            Search
          </button>
        </div>
        {err && <p className="text-red-400 text-sm mt-2">{err}</p>}
        {ok && <p className="text-green-400 text-sm mt-2">{ok}</p>}
        {importMutation.isPending && (
          <p className="text-indigo-400 text-sm mt-2 animate-pulse">
            Importing (fetching seasons & episodes — may take a moment)…
          </p>
        )}
      </div>

      {isFetching && <p className="text-gray-500 text-sm">Searching…</p>}

      {results.length > 0 && (
        <div className="space-y-2">
          {results.slice(0, 15).map(r => {
            const alreadyIn = importedTmdbIds.has(r.id)
            const displayTitle = r.title || r.name
            const year = (r.release_date || r.first_air_date || '').slice(0, 4)
            return (
              <div key={r.id} className="flex items-center gap-3 p-3 bg-gray-900 rounded-xl">
                {r.poster_path ? (
                  <img src={TMDB_POSTER + r.poster_path} alt={displayTitle} className="w-10 h-14 object-cover rounded shrink-0" />
                ) : (
                  <div className="w-10 h-14 bg-gray-800 rounded shrink-0" />
                )}
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-white truncate">{displayTitle}</p>
                  <p className="text-xs text-gray-400">{year} · ★ {r.vote_average?.toFixed(1)}</p>
                  {r.overview && <p className="text-xs text-gray-500 line-clamp-1 mt-0.5">{r.overview}</p>}
                </div>
                {alreadyIn ? (
                  <span className="text-xs text-green-400 shrink-0">✓ In library</span>
                ) : importingId === r.id ? (
                  <span className="text-xs text-indigo-400 shrink-0 animate-pulse">Importing…</span>
                ) : (
                  <button
                    onClick={() => importMutation.mutate({ r, language: lang })}
                    disabled={importMutation.isPending}
                    className="px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 rounded-lg text-sm shrink-0 transition-colors"
                  >
                    Import
                  </button>
                )}
              </div>
            )
          })}
        </div>
      )}

      {/* Library */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-lg font-semibold">Library ({imported?.total ?? 0})</h2>
          <Link to="/" className="text-sm text-indigo-400 hover:text-indigo-300">View all →</Link>
        </div>
        <div className="space-y-2 max-h-[500px] overflow-y-auto pr-1">
          {(imported?.items ?? []).map(item => (
            <div key={item.id} className="flex items-center gap-3 p-3 bg-gray-900 rounded-xl">
              {item.poster_url ? (
                <img src={item.poster_url} alt={item.title} className="w-8 h-11 object-cover rounded shrink-0" />
              ) : <div className="w-8 h-11 bg-gray-800 rounded shrink-0" />}
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-white truncate">{item.title}</p>
                <p className="text-xs text-gray-400">
                  {item.year} · {item.media_type}
                  {item.metadata_language && item.metadata_language !== 'en-US'
                    ? ` · ${item.metadata_language}`
                    : ''}
                </p>
              </div>
              <div className="flex gap-2 shrink-0">
                <Link to={`/admin/media/${item.id}/link`} className="text-xs text-indigo-400 hover:text-indigo-300">
                  Files
                </Link>
                <Link to={`/media/${item.id}`} className="text-xs text-gray-400 hover:text-white">
                  Detail
                </Link>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

// ---- Admin shell ----------------------------------------------------------

function tabClass(isActive: boolean) {
  return `px-4 py-2 text-sm font-medium rounded-lg transition-colors ${
    isActive ? 'bg-indigo-600 text-white' : 'text-gray-400 hover:text-white'
  }`
}

export default function Admin() {
  return (
    <div className="min-h-screen bg-gray-950">
      <header className="px-4 py-3 border-b border-white/5 flex items-center gap-4">
        <Link to="/" className="text-gray-400 hover:text-white transition-colors">← Library</Link>
        <span className="text-lg font-semibold text-white">Admin</span>
      </header>
      <div className="max-w-4xl mx-auto px-4 py-6">
        <nav className="flex gap-2 mb-8 flex-wrap">
          <NavLink to="/admin/media" className={({ isActive }) => tabClass(isActive)}>Media</NavLink>
          <NavLink to="/admin/containers" className={({ isActive }) => tabClass(isActive)}>Containers</NavLink>
          <NavLink to="/admin/users" className={({ isActive }) => tabClass(isActive)}>Users</NavLink>
          <NavLink to="/admin/jobs" className={({ isActive }) => tabClass(isActive)}>Jobs</NavLink>
        </nav>
        <Routes>
          <Route index element={<Navigate to="/admin/media" replace />} />
          <Route path="media" element={<AdminMedia />} />
          <Route path="containers" element={<AdminContainers />} />
          <Route path="users" element={<AdminUsers />} />
          <Route path="jobs" element={<AdminJobs />} />
        </Routes>
      </div>
    </div>
  )
}
