import { useEffect } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import Player from '../components/Player'
import { useAuth } from '../context/AuthContext'
import { useRecentlyWatched } from '../hooks/useRecentlyWatched'

function formatDuration(sec: number | null) {
  if (sec == null) return '—'
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}

function formatSize(bytes: number | null) {
  if (bytes == null) return '—'
  if (bytes < 1024 ** 3) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`
}

function formatRuntime(min: number | null | undefined) {
  if (!min) return null
  const h = Math.floor(min / 60)
  const m = min % 60
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}

export default function Watch() {
  const { id } = useParams<{ id: string }>()
  const { user } = useAuth()
  const qc = useQueryClient()
  const { record } = useRecentlyWatched()

  const { data: video, isLoading, isError } = useQuery({
    queryKey: ['video', id],
    queryFn: () => api.getVideo(Number(id)),
    enabled: !!id,
  })

  const { data: media } = useQuery({
    queryKey: ['media', video?.media_id],
    queryFn: () => api.getMedia(video!.media_id!),
    enabled: !!video?.media_id,
  })

  // Record this video as recently watched once we have enough info
  useEffect(() => {
    if (!video) return
    // Find episode/season info from media seasons if available
    let seasonNumber: number | undefined
    let episodeNumber: number | undefined
    if (media?.seasons && video.episode_id != null) {
      for (const s of media.seasons) {
        const ep = s.episodes?.find(e => e.id === video.episode_id)
        if (ep) { seasonNumber = s.season_number; episodeNumber = ep.episode_number; break }
      }
    }
    record({
      videoId: video.id,
      title: media?.title ?? video.title ?? video.filename,
      mediaId: media?.id,
      mediaTitle: media?.title,
      mediaType: media?.media_type,
      posterUrl: media?.poster_url,
      seasonNumber,
      episodeNumber,
    })
  }, [video?.id, media?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  const transcodeJob = useMutation({
    mutationFn: () => api.createJob(Number(id)),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['video', id] }),
  })

  if (isLoading) return <div className="flex items-center justify-center h-screen text-gray-400">Loading…</div>
  if (isError || !video) return <div className="text-center py-20 text-red-400">Video not found.</div>

  const title = media?.title ?? video.title ?? video.filename

  return (
    <div className="min-h-screen bg-gray-950 text-white">
      {/* Backdrop blur */}
      {media?.backdrop_url && (
        <div className="fixed inset-0 -z-10 overflow-hidden pointer-events-none">
          <img src={media.backdrop_url} alt="" className="w-full h-full object-cover opacity-8 blur-md scale-110" />
          <div className="absolute inset-0 bg-gray-950/85" />
        </div>
      )}

      <header className="px-4 py-3 border-b border-white/5 bg-gray-950/80 backdrop-blur sticky top-0 z-20 flex items-center gap-3">
        {media ? (
          <Link to={`/media/${media.id}`} className="text-gray-400 hover:text-white transition-colors">← {media.title}</Link>
        ) : (
          <Link to="/" className="text-gray-400 hover:text-white transition-colors">← Library</Link>
        )}
        <span className="text-white font-medium truncate">{title}</span>
      </header>

      {/* Player — full width, 16:9 */}
      <div className="w-full bg-black">
        <div className="max-w-6xl mx-auto">
          <Player video={video} />
        </div>
      </div>

      <main className="max-w-6xl mx-auto px-4 py-6">
        <div className="flex gap-6 flex-col md:flex-row">
          {/* Left: main info */}
          <div className="flex-1 min-w-0">
            <h1 className="text-2xl font-bold text-white">{title}</h1>

            {media?.tagline && (
              <p className="text-gray-400 italic mt-1">"{media.tagline}"</p>
            )}

            {/* Meta row */}
            <div className="flex flex-wrap items-center gap-3 mt-2 text-sm text-gray-400">
              {media?.year && <span>{media.year}</span>}
              {formatRuntime(media?.runtime) && <span>{formatRuntime(media?.runtime)}</span>}
              {media?.vote_average != null && media.vote_average > 0 && (
                <span className={`font-bold ${media.vote_average >= 7.5 ? 'text-green-400' : media.vote_average >= 6 ? 'text-yellow-400' : 'text-red-400'}`}>
                  ★ {media.vote_average.toFixed(1)}
                </span>
              )}
              {media?.language && (
                <span className="uppercase bg-gray-800 rounded px-1.5 py-0.5 text-xs">{media.language}</span>
              )}
            </div>

            {/* Genres */}
            {(media?.genres?.length ?? 0) > 0 && (
              <div className="flex flex-wrap gap-1.5 mt-3">
                {media!.genres.map(g => (
                  <span key={g.id} className="text-xs bg-indigo-600/25 text-indigo-300 border border-indigo-500/30 rounded-full px-2.5 py-0.5">{g.name}</span>
                ))}
              </div>
            )}

            {/* Overview */}
            {media?.overview && (
              <p className="text-gray-300 mt-4 leading-relaxed">{media.overview}</p>
            )}

            {/* Links */}
            <div className="flex flex-wrap gap-3 mt-4">
              {media?.imdb_url && (
                <a href={media.imdb_url} target="_blank" rel="noopener noreferrer"
                  className="flex items-center gap-1.5 text-sm text-yellow-400 hover:text-yellow-300 transition-colors">
                  <span className="font-bold bg-yellow-400 text-black rounded px-1 text-xs">IMDb</span>
                  IMDb
                </a>
              )}
              {media?.tmdb_id && (
                <a href={`https://www.themoviedb.org/${media.media_type}/${media.tmdb_id}`}
                  target="_blank" rel="noopener noreferrer"
                  className="text-sm text-blue-400 hover:text-blue-300 transition-colors">
                  TMDB ↗
                </a>
              )}
            </div>
          </div>

          {/* Right: poster + file details */}
          <div className="shrink-0 flex flex-col gap-4 md:w-56">
            {media?.poster_url && (
              <img src={media.poster_url} alt={media.title} className="w-full rounded-xl shadow-xl object-cover" />
            )}

            {/* File details */}
            <div className="rounded-xl bg-gray-900 p-4 space-y-2 text-sm">
              <div className="flex justify-between">
                <span className="text-gray-500">Duration</span>
                <span className="text-white">{formatDuration(video.duration_sec)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-500">Size</span>
                <span className="text-white">{formatSize(video.size_bytes)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-500">Format</span>
                <span className="text-white">{video.content_type || '—'}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-500">Transcode</span>
                <span className="text-white capitalize">{video.transcode_status}</span>
              </div>
            </div>

            {/* Object ID */}
            <div className="p-3 bg-gray-900 rounded-lg text-xs font-mono text-gray-600 break-all">
              {video.object_id}
            </div>

            {/* Transcode button */}
            {user?.role === 'admin' && video.transcode_status === 'none' && (
              <button
                onClick={() => transcodeJob.mutate()}
                disabled={transcodeJob.isPending}
                className="w-full px-4 py-2 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 rounded-lg text-sm font-medium transition-colors"
              >
                {transcodeJob.isPending ? 'Queuing…' : 'Queue HLS Transcode'}
              </button>
            )}
          </div>
        </div>
      </main>
    </div>
  )
}
