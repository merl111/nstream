import { useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Season, Video, Language } from '../api/client'
import { useAuth } from '../context/AuthContext'

// ---- Helpers ---------------------------------------------------------------

function formatRuntime(min: number | null | undefined) {
  if (!min) return null
  const h = Math.floor(min / 60)
  const m = min % 60
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}

function StarRating({ value }: { value: number | null }) {
  if (!value || value === 0) return null
  const color = value >= 7.5 ? 'text-green-400' : value >= 6 ? 'text-yellow-400' : 'text-red-400'
  return <span className={`font-bold ${color}`}>★ {value.toFixed(1)}</span>
}

function Badge({ children, className = '' }: { children: React.ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${className}`}>
      {children}
    </span>
  )
}

// ---- Language selector overlay ---------------------------------------------

function LanguageSelector({
  current,
  languages,
  onSelect,
  onClose,
}: {
  current: string
  languages: Language[]
  onSelect: (code: string) => void
  onClose: () => void
}) {
  const [filter, setFilter] = useState('')
  const filtered = languages.filter(
    l => l.English.toLowerCase().includes(filter.toLowerCase()) ||
         l.Native.toLowerCase().includes(filter.toLowerCase()) ||
         l.Code.toLowerCase().includes(filter.toLowerCase())
  )
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div className="bg-gray-900 rounded-2xl w-full max-w-sm mx-4 shadow-2xl overflow-hidden" onClick={e => e.stopPropagation()}>
        <div className="p-4 border-b border-white/10">
          <p className="font-semibold text-white mb-3">Select metadata language</p>
          <input
            autoFocus
            value={filter}
            onChange={e => setFilter(e.target.value)}
            placeholder="Filter…"
            className="w-full px-3 py-2 bg-gray-800 rounded-lg border border-white/10 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </div>
        <div className="max-h-72 overflow-y-auto py-2">
          {filtered.map(l => (
            <button
              key={l.Code}
              onClick={() => onSelect(l.Code)}
              className={`w-full flex items-center justify-between px-4 py-2.5 text-sm hover:bg-gray-800 transition-colors ${l.Code === current ? 'text-indigo-400' : 'text-white'}`}
            >
              <span>{l.English}</span>
              <span className="text-gray-500">{l.Native} · {l.Code}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}

// ---- Season viewer ---------------------------------------------------------

function VideoRow({ video }: { video: Video }) {
  const dur = video.duration_sec
  const durStr = dur
    ? `${Math.floor(dur / 60)}:${String(Math.floor(dur % 60)).padStart(2, '0')}`
    : null
  return (
    <Link
      to={`/watch/${video.id}`}
      className="group flex items-center gap-3 p-3 rounded-xl bg-gray-900 hover:bg-gray-800 transition-colors"
    >
      <div className="w-10 h-10 rounded-lg bg-indigo-600/20 flex items-center justify-center shrink-0">
        <svg className="w-5 h-5 text-indigo-400 group-hover:text-indigo-300" fill="currentColor" viewBox="0 0 24 24">
          <path d="M8 5v14l11-7z" />
        </svg>
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm text-white truncate">{video.filename}</p>
        {durStr && <p className="text-xs text-gray-500">{durStr}</p>}
      </div>
      <svg className="w-4 h-4 text-gray-600 shrink-0" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
        <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
      </svg>
    </Link>
  )
}

function EpisodeRow({
  ep,
  linkedVideo,
  adminLink,
}: {
  ep: NonNullable<Season['episodes']>[number]
  linkedVideo?: Video
  adminLink?: string
}) {
  const inner = (
    <div className={`flex gap-3 py-3 px-4 transition-colors ${linkedVideo ? 'hover:bg-indigo-950/40 cursor-pointer' : 'hover:bg-gray-900/50'}`}>
      {/* Still or placeholder */}
      <div className="shrink-0 relative">
        {ep.still_url ? (
          <img
            src={ep.still_url}
            alt={ep.name}
            className="w-28 h-16 object-cover rounded-lg"
            loading="lazy"
          />
        ) : (
          <div className="w-28 h-16 bg-gray-800 rounded-lg flex items-center justify-center text-gray-600 text-xs font-mono">
            E{String(ep.episode_number).padStart(2, '0')}
          </div>
        )}
        {/* Play icon overlay — always visible when linked */}
        {linkedVideo && (
          <div className="absolute inset-0 flex items-center justify-center rounded-lg bg-black/50">
            <svg className="w-8 h-8 text-white drop-shadow-lg" fill="currentColor" viewBox="0 0 24 24">
              <path d="M8 5v14l11-7z" />
            </svg>
          </div>
        )}
      </div>

      {/* Info */}
      <div className="flex-1 min-w-0">
        <div className="flex items-start justify-between gap-2">
          <p className="text-sm font-medium text-white leading-snug">
            <span className="text-gray-500 text-xs mr-1.5">
              {String(ep.episode_number).padStart(2, '0')}
            </span>
            {ep.name}
          </p>
          <div className="flex items-center gap-2 shrink-0">
            {ep.vote_average ? <StarRating value={ep.vote_average} /> : null}
            {ep.runtime && (
              <span className="text-xs text-gray-500">{ep.runtime}m</span>
            )}
          </div>
        </div>
        {ep.air_date && (
          <p className="text-xs text-gray-500 mt-0.5">{ep.air_date}</p>
        )}
        {ep.overview && (
          <p className="text-xs text-gray-400 mt-1 line-clamp-2 leading-relaxed">{ep.overview}</p>
        )}
        {/* File status */}
        <div className="mt-1.5 flex items-center gap-2">
          {linkedVideo ? (
            <Badge className="bg-green-500/15 text-green-400 border border-green-500/20">
              ▶ Play
            </Badge>
          ) : adminLink ? (
            <Link
              to={adminLink}
              onClick={e => e.stopPropagation()}
              className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
            >
              Link file →
            </Link>
          ) : (
            <Badge className="bg-gray-700/50 text-gray-500">No file</Badge>
          )}
        </div>
      </div>
    </div>
  )

  if (linkedVideo) {
    return <Link to={`/watch/${linkedVideo.id}`}>{inner}</Link>
  }
  return inner
}

function SeasonPanel({
  season,
  mediaVideos,
  adminMediaId,
}: {
  season: Season
  mediaVideos: Video[]
  adminMediaId?: number
}) {
  const [open, setOpen] = useState(season.season_number === 1)
  const linkedCount = (season.episodes ?? []).filter(ep =>
    mediaVideos.some(v => v.episode_id === ep.id)
  ).length

  return (
    <div className="rounded-2xl overflow-hidden border border-white/8">
      {/* Season header */}
      <button
        onClick={() => setOpen(o => !o)}
        className="w-full flex items-center gap-4 p-4 bg-gray-900 hover:bg-gray-800 transition-colors text-left"
      >
        <div className="shrink-0">
          {season.poster_url ? (
            <img src={season.poster_url} alt={season.name} className="w-12 h-[4.5rem] object-cover rounded-lg shadow" loading="lazy" />
          ) : (
            <div className="w-12 h-[4.5rem] bg-gray-700 rounded-lg flex items-center justify-center">
              <span className="text-xs text-gray-500 font-mono">S{season.season_number}</span>
            </div>
          )}
        </div>

        <div className="flex-1 min-w-0">
          <p className="font-semibold text-white">{season.name || `Season ${season.season_number}`}</p>
          <div className="flex items-center gap-2 mt-0.5 flex-wrap">
            <span className="text-xs text-gray-400">{season.episode_count} episodes</span>
            {season.air_date && <span className="text-xs text-gray-500">{season.air_date.slice(0, 4)}</span>}
            {linkedCount > 0 && (
              <Badge className="bg-indigo-500/15 text-indigo-400 border border-indigo-500/20">
                {linkedCount}/{season.episode_count} files
              </Badge>
            )}
          </div>
          {season.overview && open === false && (
            <p className="text-xs text-gray-500 mt-1 line-clamp-1">{season.overview}</p>
          )}
        </div>

        <svg
          className={`w-5 h-5 text-gray-500 shrink-0 transition-transform duration-200 ${open ? 'rotate-180' : ''}`}
          fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {/* Episodes */}
      {open && (
        <div className="divide-y divide-white/5 bg-gray-950/40">
          {season.overview && (
            <p className="px-4 py-3 text-sm text-gray-400 italic leading-relaxed">{season.overview}</p>
          )}
          {(season.episodes ?? []).map(ep => {
            const linked = mediaVideos.find(v => v.episode_id === ep.id)
            const adminLink = adminMediaId ? `/admin/media/${adminMediaId}/link` : undefined
            return (
              <EpisodeRow key={ep.id} ep={ep} linkedVideo={linked} adminLink={!linked ? adminLink : undefined} />
            )
          })}
        </div>
      )}
    </div>
  )
}

// ---- Main page -------------------------------------------------------------

export default function MediaDetail() {
  const { id } = useParams<{ id: string }>()
  const { user } = useAuth()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [showLangPicker, setShowLangPicker] = useState(false)

  const { data: item, isLoading } = useQuery({
    queryKey: ['media', id],
    queryFn: () => api.getMedia(Number(id)),
    enabled: !!id,
  })

  const { data: languages = [] } = useQuery({
    queryKey: ['languages'],
    queryFn: api.listLanguages,
    enabled: user?.role === 'admin',
  })

  const del = useMutation({
    mutationFn: () => api.deleteMedia(Number(id)),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['media'] }); navigate('/') },
  })

  const reimport = useMutation({
    mutationFn: (lang: string) => api.reimportMedia(Number(id), lang),
    onSuccess: () => {
      setShowLangPicker(false)
      qc.invalidateQueries({ queryKey: ['media', id] })
      qc.invalidateQueries({ queryKey: ['media'] })
    },
  })

  const autoMatch = useMutation({
    mutationFn: () => api.autoMatchVideos(Number(id)),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ['media', id] })
      qc.invalidateQueries({ queryKey: ['unlinked-videos'] })
      alert(`Auto-matched ${data.matched.length} file(s).${data.skipped.length ? `\n${data.skipped.length} file(s) skipped (no S##E## pattern found).` : ''}`)
    },
  })

  if (isLoading) return (
    <div className="flex items-center justify-center h-screen text-gray-400">Loading…</div>
  )
  if (!item) return <div className="text-center py-20 text-red-400">Not found.</div>

  const runtime = formatRuntime(item.runtime)
  const videos: Video[] = item.videos ?? []

  // Find the display name of the current metadata language
  const currentLangInfo = languages.find(l => l.Code === item.metadata_language)
  const langLabel = currentLangInfo
    ? `${currentLangInfo.Native} (${currentLangInfo.Code})`
    : item.metadata_language || 'en-US'

  return (
    <div className="min-h-screen bg-gray-950 text-white">
      {/* Backdrop */}
      {item.backdrop_url && (
        <div className="fixed inset-0 -z-10 overflow-hidden pointer-events-none">
          <img src={item.backdrop_url} alt="" className="w-full h-full object-cover opacity-10 blur-sm scale-105" />
          <div className="absolute inset-0 bg-gradient-to-b from-gray-950/50 to-gray-950" />
        </div>
      )}

      <header className="px-4 py-3 border-b border-white/5 flex items-center gap-4 backdrop-blur bg-gray-950/80 sticky top-0 z-20">
        <Link to="/" className="text-gray-400 hover:text-white transition-colors flex items-center gap-1.5 text-sm">
          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
          Library
        </Link>
        <span className="text-gray-500 text-sm">{item.media_type === 'tv' ? 'TV Show' : 'Movie'}</span>
      </header>

      <main className="max-w-5xl mx-auto px-4 py-8">

        {/* Hero section */}
        <div className="flex gap-6 flex-col sm:flex-row">
          {/* Poster */}
          <div className="shrink-0">
            <img
              src={item.poster_url || ''}
              alt={item.title}
              className="w-40 sm:w-52 rounded-2xl shadow-2xl object-cover"
              onError={e => { (e.target as HTMLImageElement).style.display = 'none' }}
            />
          </div>

          {/* Info */}
          <div className="flex-1 min-w-0">
            <h1 className="text-3xl font-bold text-white leading-tight">{item.title}</h1>
            {item.original_title && item.original_title !== item.title && (
              <p className="text-gray-400 mt-0.5">{item.original_title}</p>
            )}
            {item.tagline && (
              <p className="text-gray-400 italic mt-1.5 text-sm">"{item.tagline}"</p>
            )}

            {/* Meta row */}
            <div className="flex flex-wrap items-center gap-3 mt-3 text-sm">
              {item.year && <span className="text-gray-300 font-medium">{item.year}</span>}
              {runtime && <span className="text-gray-400">{runtime}</span>}
              {item.status && item.status !== 'Released' && (
                <Badge className="bg-blue-500/15 text-blue-300 border border-blue-500/20">{item.status}</Badge>
              )}
              <StarRating value={item.vote_average ?? null} />
              {item.vote_count ? (
                <span className="text-xs text-gray-500">({item.vote_count.toLocaleString()} votes)</span>
              ) : null}
            </div>

            {/* Languages row */}
            <div className="flex flex-wrap items-center gap-2 mt-3">
              {item.language && (
                <Badge className="bg-gray-700/60 text-gray-300 border border-white/10 uppercase tracking-wide">
                  {item.language}
                </Badge>
              )}
              {/* Metadata language — always shown, clickable for admins */}
              {user?.role === 'admin' ? (
                <button
                  onClick={() => setShowLangPicker(true)}
                  className="flex items-center gap-1.5 text-xs text-indigo-400 hover:text-indigo-300 border border-indigo-500/30 bg-indigo-500/10 rounded-full px-2.5 py-0.5 transition-colors"
                  title="Change metadata language"
                >
                  <svg className="w-3 h-3" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M3 5h12M9 3v2m1.048 9.5A18.022 18.022 0 016.412 9m6.088 9h7M11 21l5-10 5 10M12.751 5C11.783 10.77 8.07 15.61 3 18.129" />
                  </svg>
                  {langLabel}
                </button>
              ) : (
                <Badge className="bg-gray-700/40 text-gray-400 border border-white/8">
                  🌐 {langLabel}
                </Badge>
              )}
            </div>

            {/* Genres */}
            {item.genres?.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mt-3">
                {item.genres.map(g => (
                  <span key={g.id} className="text-xs bg-indigo-600/20 text-indigo-300 border border-indigo-500/25 rounded-full px-2.5 py-0.5">
                    {g.name}
                  </span>
                ))}
              </div>
            )}

            {/* Overview */}
            {item.overview && (
              <p className="text-gray-300 mt-4 leading-relaxed text-sm">{item.overview}</p>
            )}

            {/* External links */}
            <div className="flex flex-wrap gap-3 mt-4">
              {item.imdb_url && (
                <a href={item.imdb_url} target="_blank" rel="noopener noreferrer"
                  className="flex items-center gap-1.5 text-sm text-yellow-400 hover:text-yellow-300 transition-colors">
                  <span className="font-bold bg-yellow-400 text-black rounded px-1 text-xs leading-5">IMDb</span>
                  IMDb
                </a>
              )}
              {item.tmdb_id && (
                <a href={`https://www.themoviedb.org/${item.media_type}/${item.tmdb_id}`}
                  target="_blank" rel="noopener noreferrer"
                  className="text-sm text-blue-400 hover:text-blue-300 transition-colors">
                  TMDB ↗
                </a>
              )}
            </div>

            {/* Admin controls */}
            {user?.role === 'admin' && (
              <div className="mt-5 flex flex-wrap gap-2">
                <Link
                  to={`/admin/media/${item.id}/link`}
                  className="px-3 py-1.5 bg-gray-800 hover:bg-gray-700 rounded-lg text-sm transition-colors"
                >
                  Manage files
                </Link>
                {item.media_type === 'tv' && (
                  <button
                    onClick={() => autoMatch.mutate()}
                    disabled={autoMatch.isPending}
                    className="px-3 py-1.5 bg-gray-800 hover:bg-gray-700 disabled:opacity-50 rounded-lg text-sm transition-colors"
                  >
                    {autoMatch.isPending ? 'Matching…' : '⚡ Auto-match files'}
                  </button>
                )}
                <button
                  onClick={() => { if (confirm(`Delete "${item.title}"?`)) del.mutate() }}
                  className="px-3 py-1.5 bg-red-900/40 hover:bg-red-800/60 rounded-lg text-sm text-red-300 transition-colors"
                >
                  Delete
                </button>
              </div>
            )}
          </div>
        </div>

        {/* TV seasons */}
        {item.media_type === 'tv' && (item.seasons?.length ?? 0) > 0 && (
          <div className="mt-12">
            <div className="flex items-center justify-between mb-5">
              <h2 className="text-xl font-bold">
                Seasons
                <span className="text-gray-500 font-normal text-base ml-2">({item.seasons!.length})</span>
              </h2>
              {user?.role === 'admin' && (
                <Link to={`/admin/media/${item.id}/link`}
                  className="text-sm text-indigo-400 hover:text-indigo-300 transition-colors">
                  Manage episode files →
                </Link>
              )}
            </div>
            <div className="space-y-3">
              {item.seasons!.map(s => (
                <SeasonPanel
                  key={s.id}
                  season={s}
                  mediaVideos={videos}
                  adminMediaId={user?.role === 'admin' ? item.id : undefined}
                />
              ))}
            </div>
          </div>
        )}

        {/* Movie / loose video files */}
        {videos.filter(v => !v.episode_id).length > 0 && (
          <div className="mt-10">
            <h2 className="text-xl font-bold mb-4">
              {item.media_type === 'movie' ? 'Video Files' : 'Unassigned Files'}
            </h2>
            <div className="space-y-2">
              {videos.filter(v => !v.episode_id).map(v => <VideoRow key={v.id} video={v} />)}
            </div>
          </div>
        )}

        {videos.length === 0 && item.media_type === 'movie' && (
          <div className="mt-8 p-5 rounded-2xl bg-gray-900 text-center">
            <p className="text-gray-500 text-sm">No video files linked yet.</p>
            {user?.role === 'admin' && (
              <Link to={`/admin/media/${item.id}/link`} className="text-indigo-400 hover:text-indigo-300 text-sm mt-1 inline-block">
                Link a file →
              </Link>
            )}
          </div>
        )}
      </main>

      {/* Language picker */}
      {showLangPicker && (
        <LanguageSelector
          current={item.metadata_language}
          languages={languages}
          onSelect={lang => reimport.mutate(lang)}
          onClose={() => setShowLangPicker(false)}
        />
      )}

      {/* Re-importing overlay */}
      {reimport.isPending && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div className="bg-gray-900 rounded-2xl p-8 text-center shadow-2xl">
            <div className="w-10 h-10 border-4 border-indigo-500 border-t-transparent rounded-full animate-spin mx-auto mb-3" />
            <p className="text-white font-medium">Re-importing metadata…</p>
            <p className="text-gray-400 text-sm mt-1">Fetching all seasons and episodes</p>
          </div>
        </div>
      )}
    </div>
  )
}
