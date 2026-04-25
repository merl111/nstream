import { useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Video, Season, Episode } from '../api/client'

// ---- Helpers ---------------------------------------------------------------

function episodeOptions(seasons: Season[]): Array<{ label: string; ep: Episode; season: Season }> {
  const out: Array<{ label: string; ep: Episode; season: Season }> = []
  for (const s of seasons) {
    for (const ep of s.episodes ?? []) {
      out.push({
        label: `S${String(s.season_number).padStart(2, '0')}E${String(ep.episode_number).padStart(2, '0')} — ${ep.name}`,
        ep,
        season: s,
      })
    }
  }
  return out
}

// ---- File row (unlinked) ---------------------------------------------------

function UnlinkedRow({
  video,
  epOptions,
  onLink,
  linking,
}: {
  video: Video
  epOptions: ReturnType<typeof episodeOptions>
  onLink: (episodeId?: number) => void
  linking: boolean
}) {
  const [epId, setEpId] = useState('')

  const dur = video.duration_sec
  const durStr = dur ? `${Math.floor(dur / 60)}:${String(Math.floor(dur % 60)).padStart(2, '0')}` : null

  return (
    <div className="flex items-center gap-3 p-3 bg-gray-900 rounded-xl flex-wrap sm:flex-nowrap">
      <div className="w-9 h-9 rounded-lg bg-gray-800 flex items-center justify-center shrink-0">
        <svg className="w-5 h-5 text-gray-500" fill="currentColor" viewBox="0 0 24 24">
          <path d="M8 5v14l11-7z" />
        </svg>
      </div>

      <div className="flex-1 min-w-0">
        <p className="text-sm text-white truncate">{video.filename}</p>
        <p className="text-xs text-gray-500">{[video.content_type, durStr].filter(Boolean).join(' · ')}</p>
      </div>

      {/* Episode selector (only for TV) */}
      {epOptions.length > 0 && (
        <select
          value={epId}
          onChange={e => setEpId(e.target.value)}
          className="text-xs bg-gray-800 border border-white/10 rounded-lg px-2 py-1.5 text-gray-300 min-w-[180px] max-w-[260px] focus:outline-none focus:ring-2 focus:ring-indigo-500"
        >
          <option value="">No episode</option>
          {epOptions.map(eo => (
            <option key={eo.ep.id} value={String(eo.ep.id)}>{eo.label}</option>
          ))}
        </select>
      )}

      <button
        onClick={() => onLink(epId ? Number(epId) : undefined)}
        disabled={linking}
        className="px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 rounded-lg text-sm transition-colors shrink-0"
      >
        {linking ? '…' : 'Link'}
      </button>
    </div>
  )
}

// ---- Linked file row -------------------------------------------------------

function LinkedRow({
  video,
  epOptions,
  onUnlink,
}: {
  video: Video
  epOptions: ReturnType<typeof episodeOptions>
  onUnlink: () => void
}) {
  const epLabel = video.episode_id
    ? epOptions.find(e => e.ep.id === video.episode_id)?.label
    : null

  return (
    <div className="flex items-center gap-3 p-3 bg-gray-900 rounded-xl">
      <div className="w-9 h-9 rounded-lg bg-green-500/15 flex items-center justify-center shrink-0">
        <svg className="w-5 h-5 text-green-400" fill="currentColor" viewBox="0 0 24 24">
          <path d="M8 5v14l11-7z" />
        </svg>
      </div>

      <div className="flex-1 min-w-0">
        <p className="text-sm text-white truncate">{video.filename}</p>
        {epLabel && <p className="text-xs text-indigo-300 mt-0.5">{epLabel}</p>}
      </div>

      <Link to={`/watch/${video.id}`} className="text-xs text-gray-400 hover:text-white shrink-0 transition-colors">
        Watch
      </Link>
      <button
        onClick={onUnlink}
        className="text-xs text-red-400 hover:text-red-300 shrink-0 px-2 py-1 rounded transition-colors"
      >
        Unlink
      </button>
    </div>
  )
}

// ---- Main page -------------------------------------------------------------

export default function AdminMediaLink() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const mediaID = Number(id)

  const { data: item, isLoading } = useQuery({
    queryKey: ['media', id],
    queryFn: () => api.getMedia(mediaID),
    enabled: !!id,
  })
  const { data: unlinked } = useQuery({
    queryKey: ['unlinked-videos'],
    queryFn: () => api.listUnlinkedVideos(),
  })

  const [linkingId, setLinkingId] = useState<number | null>(null)

  const link = useMutation({
    mutationFn: ({ videoId, episodeId }: { videoId: number; episodeId?: number }) => {
      setLinkingId(videoId)
      return api.linkVideo(videoId, mediaID, episodeId)
    },
    onSettled: () => setLinkingId(null),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['media', id] })
      qc.invalidateQueries({ queryKey: ['unlinked-videos'] })
    },
  })
  const unlink = useMutation({
    mutationFn: (videoId: number) => api.unlinkVideo(videoId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['media', id] })
      qc.invalidateQueries({ queryKey: ['unlinked-videos'] })
    },
  })

  const autoMatch = useMutation({
    mutationFn: () => api.autoMatchVideos(mediaID),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ['media', id] })
      qc.invalidateQueries({ queryKey: ['unlinked-videos'] })
      const msg = data.matched.length === 0
        ? 'No files matched (filenames need S##E## patterns).'
        : `Matched ${data.matched.length} file(s) automatically!\n` +
          data.matched.map(m => `  S${m.season}E${m.episode}: ${m.filename}`).join('\n')
      alert(msg)
    },
  })

  if (isLoading) return <div className="min-h-screen bg-gray-950 flex items-center justify-center text-gray-400">Loading…</div>
  if (!item) return <div className="min-h-screen bg-gray-950 text-center py-20 text-red-400">Not found.</div>

  const epOpts = item.media_type === 'tv' ? episodeOptions(item.seasons ?? []) : []
  const linkedVideos: Video[] = item.videos ?? []
  const unlinkedVideos: Video[] = unlinked?.videos ?? []

  // Season overview for TV (which episodes have files, which don't)
  const episodeFileMap = new Map<number, Video>()
  for (const v of linkedVideos) {
    if (v.episode_id) episodeFileMap.set(v.episode_id, v)
  }

  return (
    <div className="min-h-screen bg-gray-950 text-white">
      <header className="px-4 py-3 border-b border-white/5 flex items-center gap-4 sticky top-0 bg-gray-950/90 backdrop-blur z-20">
        <Link to={`/media/${item.id}`} className="text-gray-400 hover:text-white transition-colors flex items-center gap-1.5 text-sm">
          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
          {item.title}
        </Link>
        <span className="text-white font-medium">Manage Files</span>
      </header>

      <main className="max-w-3xl mx-auto px-4 py-8 space-y-8">

        {/* TV overview grid */}
        {item.media_type === 'tv' && (item.seasons?.length ?? 0) > 0 && (
          <section>
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-lg font-semibold">Season Overview</h2>
              <button
                onClick={() => autoMatch.mutate()}
                disabled={autoMatch.isPending}
                className="flex items-center gap-1.5 px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 rounded-lg text-sm transition-colors"
              >
                <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
                </svg>
                {autoMatch.isPending ? 'Matching…' : 'Auto-match by filename'}
              </button>
            </div>
            <p className="text-xs text-gray-500 mb-4">
              Auto-match scans unlinked file names for S##E## patterns (e.g. "Breaking.Bad.S03E04.mkv") and links them automatically.
            </p>
            <div className="space-y-4">
              {(item.seasons ?? []).map(season => {
                const eps = season.episodes ?? []
                const linked = eps.filter(ep => episodeFileMap.has(ep.id)).length
                const pct = eps.length > 0 ? Math.round((linked / eps.length) * 100) : 0
                return (
                  <div key={season.id} className="bg-gray-900 rounded-xl p-4">
                    <div className="flex items-center justify-between gap-3 mb-2">
                      <div className="flex items-center gap-3">
                        {season.poster_url ? (
                          <img src={season.poster_url} alt={season.name} className="w-8 h-11 object-cover rounded" loading="lazy" />
                        ) : (
                          <div className="w-8 h-11 bg-gray-800 rounded flex items-center justify-center text-xs text-gray-500">S{season.season_number}</div>
                        )}
                        <div>
                          <p className="text-sm font-medium text-white">{season.name || `Season ${season.season_number}`}</p>
                          <p className="text-xs text-gray-400">{linked}/{eps.length} episodes linked</p>
                        </div>
                      </div>
                      <span className={`text-xs font-medium ${pct === 100 ? 'text-green-400' : pct > 0 ? 'text-yellow-400' : 'text-gray-600'}`}>
                        {pct}%
                      </span>
                    </div>
                    {/* Progress bar */}
                    <div className="h-1.5 bg-gray-800 rounded-full overflow-hidden">
                      <div
                        className={`h-full rounded-full transition-all ${pct === 100 ? 'bg-green-500' : 'bg-indigo-500'}`}
                        style={{ width: `${pct}%` }}
                      />
                    </div>
                    {/* Episode chips */}
                    <div className="flex flex-wrap gap-1 mt-3">
                      {eps.map(ep => {
                        const hasFile = episodeFileMap.has(ep.id)
                        return (
                          <span
                            key={ep.id}
                            title={ep.name}
                            className={`text-xs px-1.5 py-0.5 rounded font-mono ${hasFile ? 'bg-green-500/20 text-green-400' : 'bg-gray-800 text-gray-600'}`}
                          >
                            E{String(ep.episode_number).padStart(2, '0')}
                          </span>
                        )
                      })}
                    </div>
                  </div>
                )
              })}
            </div>
          </section>
        )}

        {/* Currently linked files */}
        {linkedVideos.length > 0 && (
          <section>
            <h2 className="text-lg font-semibold mb-3">
              Linked Files <span className="text-gray-500 font-normal text-base">({linkedVideos.length})</span>
            </h2>
            <div className="space-y-2">
              {linkedVideos.map(v => (
                <LinkedRow
                  key={v.id}
                  video={v}
                  epOptions={epOpts}
                  onUnlink={() => unlink.mutate(v.id)}
                />
              ))}
            </div>
          </section>
        )}

        {/* Unlinked files */}
        <section>
          <h2 className="text-lg font-semibold mb-1">
            Available Files
            <span className="text-gray-500 font-normal text-base ml-2">({unlinked?.total ?? 0} unlinked)</span>
          </h2>
          {item.media_type === 'tv' && (
            <p className="text-xs text-gray-500 mb-3">
              Select an episode from the dropdown before linking, or use Auto-match above.
            </p>
          )}
          {unlinkedVideos.length === 0 && (
            <p className="text-gray-500 text-sm py-4">No unlinked files — upload videos first.</p>
          )}
          <div className="space-y-2">
            {unlinkedVideos.map(v => (
              <UnlinkedRow
                key={v.id}
                video={v}
                epOptions={epOpts}
                onLink={(epId) => link.mutate({ videoId: v.id, episodeId: epId })}
                linking={linkingId === v.id}
              />
            ))}
          </div>
        </section>
      </main>
    </div>
  )
}
