import { useState, useCallback, useEffect, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { api } from '../api/client'
import type { MediaItem, Video, Genre, UploadJob } from '../api/client'
import { useAuth } from '../context/AuthContext'
import UploadModal from '../components/UploadModal'
import { useRecentlyWatched, type WatchedEntry } from '../hooks/useRecentlyWatched'

// ---- helpers ---------------------------------------------------------------

const PLACEHOLDER = 'data:image/svg+xml,%3Csvg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 300" fill="%23374151"%3E%3Crect width="200" height="300"/%3E%3Ctext x="50%25" y="50%25" dominant-baseline="middle" text-anchor="middle" font-size="48" fill="%236B7280"%3E🎬%3C/text%3E%3C/svg%3E'

function ratingColor(r: number) {
  if (r >= 7.5) return 'text-green-400'
  if (r >= 6) return 'text-yellow-400'
  return 'text-red-400'
}

// ---- Media poster card ------------------------------------------------------

function MediaCard({ item }: { item: MediaItem }) {
  const navigate = useNavigate()
  return (
    <div
      className="group relative cursor-pointer rounded-xl overflow-hidden bg-gray-900 hover:ring-2 hover:ring-indigo-500 transition-all duration-200"
      onClick={() => navigate(`/media/${item.id}`)}
    >
      <div className="aspect-[2/3] overflow-hidden">
        <img
          src={item.poster_url || PLACEHOLDER}
          alt={item.title}
          loading="lazy"
          className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-300"
          onError={e => { (e.target as HTMLImageElement).src = PLACEHOLDER }}
        />
      </div>
      {/* Overlay on hover */}
      <div className="absolute inset-0 bg-gradient-to-t from-black/90 via-black/20 to-transparent opacity-0 group-hover:opacity-100 transition-opacity duration-200 flex flex-col justify-end p-3">
        {item.overview && (
          <p className="text-xs text-gray-300 line-clamp-4 mb-2">{item.overview}</p>
        )}
        <div className="flex flex-wrap gap-1">
          {item.genres?.slice(0, 3).map(g => (
            <span key={g.id} className="text-xs bg-indigo-600/80 rounded px-1.5 py-0.5">{g.name}</span>
          ))}
        </div>
      </div>
      {/* Rating badge */}
      {item.vote_average != null && item.vote_average > 0 && (
        <div className={`absolute top-2 right-2 text-xs font-bold bg-black/70 rounded px-1.5 py-0.5 ${ratingColor(item.vote_average)}`}>
          ★ {item.vote_average.toFixed(1)}
        </div>
      )}
      {/* Type badge */}
      <div className="absolute top-2 left-2 text-xs bg-black/70 rounded px-1.5 py-0.5 text-gray-300">
        {item.media_type === 'tv' ? 'TV' : 'Movie'}
      </div>
      <div className="p-2">
        <p className="text-sm font-medium text-white truncate">{item.title}</p>
        <p className="text-xs text-gray-400">{item.year}</p>
      </div>
    </div>
  )
}

// ---- Video-only card (for raw files without metadata) ----------------------

function VideoCard({ video, isAdmin }: { video: Video; isAdmin: boolean }) {
  const qc = useQueryClient()
  const [matchLabel, setMatchLabel] = useState<string | null>(null)

  function fmtSize(b: number | null) {
    if (!b) return ''
    if (b < 1024 ** 2) return `${(b / 1024).toFixed(0)} KB`
    if (b < 1024 ** 3) return `${(b / 1024 ** 2).toFixed(1)} MB`
    return `${(b / 1024 ** 3).toFixed(2)} GB`
  }
  function fmtDur(s: number | null) {
    if (!s) return ''
    const m = Math.floor(s / 60)
    const sec = Math.floor(s % 60)
    return `${m}:${String(sec).padStart(2, '0')}`
  }

  const match = useMutation({
    mutationFn: () => api.matchVideo(video.id),
    onSuccess: (data) => {
      if (data.matched) {
        setMatchLabel(data.title ?? 'Matched')
        qc.invalidateQueries({ queryKey: ['videos'] })
      } else {
        setMatchLabel(data.title ? `No match for "${data.title}"` : 'No match found')
      }
    },
    onError: () => setMatchLabel('Match failed'),
  })

  return (
    <div className="group flex flex-col rounded-xl overflow-hidden bg-gray-900 hover:ring-2 hover:ring-indigo-500 transition-all duration-200">
      <Link to={`/watch/${video.id}`} className="block">
        <div className="aspect-[16/9] bg-gray-800 flex items-center justify-center overflow-hidden">
          <svg className="w-12 h-12 text-gray-600 group-hover:text-indigo-400 transition-colors" fill="currentColor" viewBox="0 0 24 24">
            <path d="M8 5v14l11-7z" />
          </svg>
        </div>
      </Link>
      <div className="p-3 flex-1 flex flex-col gap-1">
        <Link to={`/watch/${video.id}`} className="block">
          <p className="text-sm font-medium text-white truncate">{video.filename}</p>
          <p className="text-xs text-gray-500 mt-0.5">
            {[fmtDur(video.duration_sec), fmtSize(video.size_bytes)].filter(Boolean).join(' · ')}
          </p>
        </Link>
        {matchLabel && (
          <p className={`text-xs truncate ${matchLabel.startsWith('No') || matchLabel === 'Match failed' ? 'text-gray-500' : 'text-green-400'}`}>
            {matchLabel}
          </p>
        )}
        {isAdmin && !matchLabel && (
          <button
            onClick={() => match.mutate()}
            disabled={match.isPending}
            className="mt-1 flex items-center gap-1 text-xs text-indigo-400 hover:text-indigo-300 disabled:opacity-50 transition-colors self-start"
            title="Probe metadata + search TMDB"
          >
            <svg className="w-3 h-3" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
            {match.isPending ? 'Matching…' : 'Auto-match'}
          </button>
        )}
      </div>
    </div>
  )
}

// ---- Continue Watching row -------------------------------------------------

function ContinueWatching({ entries }: { entries: WatchedEntry[] }) {
  if (entries.length === 0) return null
  return (
    <div className="mb-8">
      <h2 className="text-sm font-semibold uppercase tracking-wider text-gray-500 mb-3 px-1">Continue Watching</h2>
      <div className="flex gap-3 overflow-x-auto pb-2 scrollbar-thin">
        {entries.map(e => (
          <Link
            key={e.videoId}
            to={`/watch/${e.videoId}`}
            className="shrink-0 w-44 group rounded-xl overflow-hidden bg-gray-900 hover:ring-2 hover:ring-indigo-500 transition-all"
          >
            {e.posterUrl ? (
              <div className="relative aspect-[2/3] overflow-hidden">
                <img src={e.posterUrl} alt={e.mediaTitle ?? e.title} className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-300" loading="lazy" />
                <div className="absolute inset-0 bg-gradient-to-t from-black/80 via-transparent to-transparent flex items-end p-2">
                  <div className="w-8 h-8 rounded-full bg-white/20 backdrop-blur flex items-center justify-center">
                    <svg className="w-4 h-4 text-white ml-0.5" fill="currentColor" viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg>
                  </div>
                </div>
              </div>
            ) : (
              <div className="aspect-[2/3] bg-gray-800 flex items-center justify-center">
                <svg className="w-10 h-10 text-gray-600 group-hover:text-indigo-400 transition-colors" fill="currentColor" viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg>
              </div>
            )}
            <div className="p-2">
              <p className="text-xs font-medium text-white truncate">{e.mediaTitle ?? e.title}</p>
              {e.seasonNumber != null && e.episodeNumber != null ? (
                <p className="text-xs text-gray-500">S{String(e.seasonNumber).padStart(2,'0')} E{String(e.episodeNumber).padStart(2,'0')}</p>
              ) : (
                <p className="text-xs text-gray-500 truncate">{e.title !== e.mediaTitle ? e.title : ''}</p>
              )}
            </div>
          </Link>
        ))}
      </div>
    </div>
  )
}

// ---- Genre filter sidebar --------------------------------------------------

function GenreFilter({ genres, selected, onChange }: {
  genres: Genre[]
  selected: number | null
  onChange: (id: number | null) => void
}) {
  if (genres.length === 0) return null
  return (
    <div className="hidden lg:block w-48 shrink-0">
      <p className="text-xs font-semibold uppercase tracking-wider text-gray-500 mb-2 px-2">Genres</p>
      <button
        onClick={() => onChange(null)}
        className={`w-full text-left px-3 py-1.5 rounded-lg text-sm mb-0.5 transition-colors ${selected === null ? 'bg-indigo-600 text-white' : 'text-gray-400 hover:bg-gray-800 hover:text-white'}`}
      >
        All
      </button>
      {genres.map(g => (
        <button
          key={g.id}
          onClick={() => onChange(g.id)}
          className={`w-full text-left px-3 py-1.5 rounded-lg text-sm mb-0.5 transition-colors ${selected === g.id ? 'bg-indigo-600 text-white' : 'text-gray-400 hover:bg-gray-800 hover:text-white'}`}
        >
          {g.name}
        </button>
      ))}
    </div>
  )
}

// ---- Active upload jobs indicator -------------------------------------------

function ActiveUploads() {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  const { data: jobs = [] } = useQuery<UploadJob[]>({
    queryKey: ['uploadJobs'],
    queryFn: api.listUploadJobs,
    refetchInterval: (query) => {
      const jobs = query.state.data ?? []
      return jobs.some(j => j.status !== 'done' && j.status !== 'error') ? 1500 : false
    },
    staleTime: 0,
  })

  // Close dropdown on outside click.
  useEffect(() => {
    function onClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [])

  const active = jobs.filter(j => j.status !== 'done' && j.status !== 'error')
  const recent = jobs.slice(0, 8)

  if (jobs.length === 0) return null

  function statusLabel(j: UploadJob) {
    if (j.status === 'neofs') return `NeoFS ${j.pct ?? 0}%`
    if (j.status === 'probing') return 'Reading metadata…'
    if (j.status === 'matching') return 'Matching…'
    if (j.status === 'done') return j.media_title ? `✓ ${j.media_title}` : '✓ Done'
    if (j.status === 'error') return `✗ ${j.error ?? 'Error'}`
    return 'Queued'
  }

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen(o => !o)}
        className={`relative flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg text-sm transition-colors ${open ? 'bg-gray-700 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800'}`}
        title="Upload jobs"
      >
        {/* Cloud-upload icon */}
        <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" />
        </svg>
        {active.length > 0 && (
          <span className="absolute -top-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full bg-indigo-500 text-[10px] font-bold text-white">
            {active.length}
            <span className="absolute inline-flex h-full w-full rounded-full bg-indigo-400 opacity-75 animate-ping" />
          </span>
        )}
      </button>

      {open && (
        <div className="absolute right-0 top-10 z-50 w-80 bg-gray-900 border border-white/10 rounded-xl shadow-2xl overflow-hidden">
          <div className="px-3 py-2 border-b border-white/5 text-xs font-semibold uppercase tracking-wider text-gray-500">
            Upload jobs {active.length > 0 && <span className="text-indigo-400">· {active.length} active</span>}
          </div>
          <ul className="divide-y divide-white/5 max-h-72 overflow-y-auto">
            {recent.map(j => (
              <li key={j.id} className="px-3 py-2.5">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm text-white truncate flex-1">{j.filename}</span>
                  <span className={`text-xs shrink-0 ${j.status === 'done' ? 'text-green-400' : j.status === 'error' ? 'text-red-400' : 'text-indigo-400'}`}>
                    {statusLabel(j)}
                  </span>
                </div>
                {j.status === 'neofs' && (
                  <div className="mt-1 h-1 bg-gray-700 rounded-full overflow-hidden">
                    <div
                      className="h-full bg-indigo-500 rounded-full transition-all duration-500"
                      style={{ width: `${j.pct ?? 0}%` }}
                    />
                  </div>
                )}
                {(j.status === 'probing' || j.status === 'matching' || j.status === 'queued') && (
                  <div className="mt-1 h-1 bg-gray-700 rounded-full overflow-hidden">
                    <div className="h-full w-1/3 bg-indigo-500 rounded-full animate-pulse" />
                  </div>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

// ---- Main page -------------------------------------------------------------

type Tab = 'media' | 'movies' | 'tv' | 'files'

export default function Library() {
  const { user, logout } = useAuth()
  const [tab, setTab] = useState<Tab>('media')
  const [q, setQ] = useState('')
  const [debouncedQ, setDebouncedQ] = useState('')
  const [page, setPage] = useState(1)
  const [selectedGenre, setSelectedGenre] = useState<number | null>(null)
  const [showUpload, setShowUpload] = useState(false)
  const { getAll } = useRecentlyWatched()
  const [recentlyWatched, setRecentlyWatched] = useState<WatchedEntry[]>(() => getAll())

  useEffect(() => {
    const refresh = () => setRecentlyWatched(getAll())
    window.addEventListener('nstream:watched', refresh)
    window.addEventListener('storage', refresh)
    return () => { window.removeEventListener('nstream:watched', refresh); window.removeEventListener('storage', refresh) }
  }, [getAll])

  // Debounce search input
  const handleSearch = useCallback((v: string) => {
    setQ(v)
    clearTimeout((handleSearch as { t?: ReturnType<typeof setTimeout> }).t)
    ;(handleSearch as { t?: ReturnType<typeof setTimeout> }).t = setTimeout(() => {
      setDebouncedQ(v)
      setPage(1)
    }, 300)
  }, [])

  const mediaType = tab === 'movies' ? 'movie' : tab === 'tv' ? 'tv' : tab === 'media' ? '' : undefined

  const { data: mediaData } = useQuery({
    queryKey: ['media', mediaType, debouncedQ, selectedGenre, page],
    queryFn: () => api.listMedia({ type: mediaType ?? '', q: debouncedQ, genre: selectedGenre ?? undefined, page, limit: 40 }),
    enabled: tab !== 'files',
  })

  const { data: videosData } = useQuery({
    queryKey: ['videos', debouncedQ, page],
    queryFn: () => api.listVideos({ q: debouncedQ, page, limit: 40 }),
    enabled: tab === 'files',
  })

  const { data: genresRaw } = useQuery({
    queryKey: ['genres'],
    queryFn: api.listGenres,
  })
  const genres = genresRaw ?? []

  const tabs: { key: Tab; label: string }[] = [
    { key: 'media', label: 'All' },
    { key: 'movies', label: 'Movies' },
    { key: 'tv', label: 'TV Shows' },
    { key: 'files', label: 'Raw Files' },
  ]

  const totalPages = tab === 'files'
    ? Math.ceil((videosData?.total ?? 0) / 40)
    : Math.ceil((mediaData?.total ?? 0) / 40)

  return (
    <div className="min-h-screen bg-gray-950 text-white">
      {/* Header */}
      <header className="sticky top-0 z-30 bg-gray-950/90 backdrop-blur border-b border-white/5 px-4 py-3">
        <div className="max-w-screen-2xl mx-auto flex items-center gap-3 flex-wrap">
          <span className="text-xl font-bold text-indigo-400 mr-2">nstream</span>

          {/* Tabs */}
          <div className="flex gap-1 rounded-lg bg-gray-900 p-1">
            {tabs.map(t => (
              <button
                key={t.key}
                onClick={() => { setTab(t.key); setPage(1); setSelectedGenre(null) }}
                className={`px-3 py-1 rounded-md text-sm font-medium transition-colors ${tab === t.key ? 'bg-indigo-600 text-white' : 'text-gray-400 hover:text-white'}`}
              >
                {t.label}
              </button>
            ))}
          </div>

          {/* Search */}
          <div className="flex-1 min-w-[180px] max-w-xs">
            <input
              type="search"
              value={q}
              onChange={e => handleSearch(e.target.value)}
              placeholder="Search…"
              className="w-full px-3 py-1.5 rounded-lg bg-gray-800 border border-white/10 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            />
          </div>

          <div className="ml-auto flex items-center gap-3">
            {user?.role === 'admin' && (
              <>
                <ActiveUploads />
                <button
                  onClick={() => setShowUpload(true)}
                  className="flex items-center gap-1.5 px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 rounded-lg text-sm font-medium transition-colors"
                >
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M4 16v2a2 2 0 002 2h12a2 2 0 002-2v-2M16 10l-4-4m0 0L8 10m4-4v12" />
                  </svg>
                  Upload
                </button>
                <Link to="/admin" className="text-sm text-gray-400 hover:text-white transition-colors">Admin</Link>
              </>
            )}
            <button onClick={() => logout()} className="text-sm text-gray-400 hover:text-white transition-colors">Sign out</button>
          </div>
        </div>
      </header>

      <div className="max-w-screen-2xl mx-auto px-4 py-6 flex gap-6">
        {/* Genre sidebar (only for media tabs) */}
        {tab !== 'files' && (
          <GenreFilter genres={genres} selected={selectedGenre} onChange={id => { setSelectedGenre(id); setPage(1) }} />
        )}

        <div className="flex-1 min-w-0">
          {/* Continue Watching */}
          {tab !== 'files' && recentlyWatched.length > 0 && !debouncedQ && selectedGenre === null && page === 1 && (
            <ContinueWatching entries={recentlyWatched} />
          )}

          {/* Media grid */}
          {tab !== 'files' && (
            <>
              {mediaData?.items?.length === 0 && (
                <div className="text-center py-20 text-gray-500">
                  {debouncedQ ? 'No results.' : 'No media yet — go to Admin to import from TMDB.'}
                </div>
              )}
              <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 2xl:grid-cols-7 gap-3">
                {mediaData?.items?.map(item => (
                  <MediaCard key={item.id} item={item} />
                ))}
              </div>
            </>
          )}

          {/* Raw files grid */}
          {tab === 'files' && (
            <>
              {videosData?.videos?.length === 0 && (
                <div className="text-center py-20 text-gray-500">No files yet.</div>
              )}
              <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-3">
                {videosData?.videos?.map(v => <VideoCard key={v.id} video={v} isAdmin={user?.role === 'admin'} />)}
              </div>
            </>
          )}

          {/* Pagination */}
          {totalPages > 1 && (
            <div className="flex items-center justify-center gap-2 mt-8">
              <button
                disabled={page <= 1}
                onClick={() => setPage(p => p - 1)}
                className="px-3 py-1.5 rounded-lg bg-gray-800 text-sm disabled:opacity-40 hover:bg-gray-700 transition-colors"
              >
                ← Prev
              </button>
              <span className="text-sm text-gray-400">Page {page} / {totalPages}</span>
              <button
                disabled={page >= totalPages}
                onClick={() => setPage(p => p + 1)}
                className="px-3 py-1.5 rounded-lg bg-gray-800 text-sm disabled:opacity-40 hover:bg-gray-700 transition-colors"
              >
                Next →
              </button>
            </div>
          )}
        </div>
      </div>

      {showUpload && <UploadModal onClose={() => setShowUpload(false)} />}
    </div>
  )
}
