export interface User {
  id: number
  username: string
  role: 'admin' | 'viewer'
}

export interface Video {
  id: number
  container_id: number
  container_cid: string
  object_id: string
  filename: string
  title: string
  duration_sec: number | null
  size_bytes: number | null
  content_type: string
  thumbnail_oid: string
  transcode_status: 'none' | 'pending' | 'running' | 'done' | 'failed'
  transcode_oid: string
  media_id: number | null
  episode_id: number | null
  indexed_at: string
  stream_url: string
  hls_url: string
}

export interface Genre {
  id: number
  tmdb_id: number | null
  name: string
}

export interface MediaItem {
  id: number
  tmdb_id: number | null
  imdb_id: string
  media_type: 'movie' | 'tv'
  title: string
  original_title: string
  overview: string
  tagline: string
  release_date: string
  year: string
  status: string
  poster_url: string
  backdrop_url: string
  vote_average: number | null
  vote_count: number | null
  runtime: number | null
  language: string
  metadata_language: string
  genres: Genre[]
  seasons?: Season[]
  imdb_url: string
  videos?: Video[]
}

export interface Language {
  Code: string
  English: string
  Native: string
}

export interface Season {
  id: number
  season_number: number
  name: string
  overview: string
  poster_url: string
  air_date: string
  episode_count: number
  episodes?: Episode[]
}

export interface Episode {
  id: number
  episode_number: number
  name: string
  overview: string
  still_url: string
  air_date: string
  runtime: number | null
  vote_average: number | null
}

export interface MediaListResponse {
  total: number
  page: number
  limit: number
  items: MediaItem[]
}

export interface TMDBSearchResult {
  id: number
  media_type: 'movie' | 'tv'
  title: string
  name: string
  overview: string
  release_date: string
  first_air_date: string
  poster_path: string
  vote_average: number
  popularity: number
}

export interface VideoListResponse {
  total: number
  page: number
  limit: number
  videos: Video[]
}

export interface UploadJob {
  id: string
  filename: string
  status: 'queued' | 'neofs' | 'probing' | 'matching' | 'done' | 'error'
  pct?: number
  video_id?: number
  media_id?: number
  media_title?: string
  error?: string
  created_at: string
  updated_at: string
}

export interface Container {
  id: number
  cid: string
  name: string
  scan_enabled: boolean
  last_scanned_at: string | null
  created_at: string
}

export interface Job {
  id: number
  video_id: number
  status: 'pending' | 'running' | 'done' | 'failed'
  profile: string
  error: string
  started_at: string | null
  finished_at: string | null
  created_at: string
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
    credentials: 'include',
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const api = {
  // Auth
  login: (username: string, password: string) =>
    request<User>('POST', '/api/v1/auth/login', { username, password }),
  logout: () => request<void>('POST', '/api/v1/auth/logout'),
  me: () => request<User>('GET', '/api/v1/auth/me'),

  // Videos
  listVideos: (params?: { q?: string; page?: number; limit?: number }) => {
    const qs = new URLSearchParams()
    if (params?.q) qs.set('q', params.q)
    if (params?.page) qs.set('page', String(params.page))
    if (params?.limit) qs.set('limit', String(params.limit))
    return request<VideoListResponse>('GET', `/api/v1/videos?${qs}`)
  },
  getVideo: (id: number) => request<Video>('GET', `/api/v1/videos/${id}`),
  matchVideo: (id: number) => request<{
    matched: boolean
    title?: string
    media_id?: number
    tmdb_id?: number
    is_tv?: boolean
    season?: number
    episode?: number
  }>('POST', `/api/v1/videos/${id}/match`),

  // Containers
  listContainers: () => request<Container[]>('GET', '/api/v1/containers'),
  addContainer: (cid: string, name: string) =>
    request<Container>('POST', '/api/v1/containers', { cid, name }),
  createNeoFSContainer: (name: string, replicas: number, publicRead: boolean) =>
    request<Container>('POST', '/api/v1/containers/create', { name, replicas, public_read: publicRead }),
  deleteContainer: (id: number) => request<void>('DELETE', `/api/v1/containers/${id}`),
  scanContainer: (id: number) => request<void>('POST', `/api/v1/containers/${id}/scan`),

  // Upload
  uploadVideo: (
    containerID: number,
    file: File,
    onEvent?: (ev: {
      phase: string
      pct?: number
      bytes?: number
      job_id?: string
      title?: string
      is_tv?: boolean
      season?: number
      episode?: number
      media_id?: number
      tmdb_id?: number
    }) => void,
  ): Promise<string> => { // resolves with job_id once queued
    return new Promise((resolve, reject) => {
      const fd = new FormData()
      fd.append('container_id', String(containerID))
      fd.append('file', file)

      fetch('/api/v1/upload', { method: 'POST', body: fd, credentials: 'include' })
        .then(res => {
          if (!res.ok || !res.body) {
            return res.json().then(j => { throw new Error(j.error ?? res.statusText) })
          }
          const reader = res.body.getReader()
          const decoder = new TextDecoder()
          let buf = ''

          function pump(): Promise<void> {
            return reader.read().then(({ done, value }) => {
              if (done) {
                // Server closed the stream after sending queued — that's fine.
                reject(new Error('Stream ended before queued event'))
                return
              }
              buf += decoder.decode(value, { stream: true })
              const frames = buf.split('\n\n')
              buf = frames.pop() ?? ''
              for (const frame of frames) {
                const line = frame.split('\n').find(l => l.startsWith('data: '))
                if (!line) continue
                try {
                  const ev = JSON.parse(line.slice(6))
                  onEvent?.(ev)
                  if (ev.phase === 'queued') { resolve(ev.job_id ?? ''); return }
                  if (ev.phase === 'error') { reject(new Error(ev.error)); return }
                } catch { /* malformed frame, skip */ }
              }
              return pump()
            })
          }
          return pump()
        })
        .catch(reject)
    })
  },

  // Users
  listUsers: () => request<User[]>('GET', '/api/v1/users'),
  createUser: (username: string, password: string, role: string) =>
    request<User>('POST', '/api/v1/users', { username, password, role }),
  deleteUser: (id: number) => request<void>('DELETE', `/api/v1/users/${id}`),

  // Jobs
  listJobs: () => request<Job[]>('GET', '/api/v1/jobs'),
  createJob: (video_id: number) => request<Job>('POST', '/api/v1/jobs', { video_id }),
  getJob: (id: number) => request<Job>('GET', `/api/v1/jobs/${id}`),

  // Genres
  listGenres: () => request<Genre[]>('GET', '/api/v1/genres'),

  // Upload jobs
  listUploadJobs: () => request<UploadJob[]>('GET', '/api/v1/upload/jobs'),

  // Media library
  listMedia: (params?: { type?: string; q?: string; genre?: number; page?: number; limit?: number }) => {
    const qs = new URLSearchParams()
    if (params?.type) qs.set('type', params.type)
    if (params?.q) qs.set('q', params.q)
    if (params?.genre) qs.set('genre', String(params.genre))
    if (params?.page) qs.set('page', String(params.page))
    if (params?.limit) qs.set('limit', String(params.limit))
    return request<MediaListResponse>('GET', `/api/v1/media?${qs}`)
  },
  getMedia: (id: number) => request<MediaItem>('GET', `/api/v1/media/${id}`),
  deleteMedia: (id: number) => request<void>('DELETE', `/api/v1/media/${id}`),
  importMedia: (tmdb_id: number, media_type: 'movie' | 'tv', language?: string) =>
    request<MediaItem>('POST', '/api/v1/media/import', { tmdb_id, media_type, language }),
  reimportMedia: (id: number, language: string) =>
    request<MediaItem>('POST', `/api/v1/media/${id}/reimport`, { language }),
  autoMatchVideos: (mediaId: number) =>
    request<{ matched: Array<{ video_id: number; filename: string; season: number; episode: number; episode_id: number }>; skipped: string[] }>('POST', `/api/v1/media/${mediaId}/automatch`),

  // TMDB
  tmdbSearch: (q: string, type?: string, year?: string, lang?: string) => {
    const qs = new URLSearchParams({ q })
    if (type) qs.set('type', type)
    if (year) qs.set('year', year)
    if (lang) qs.set('lang', lang)
    return request<TMDBSearchResult[]>('GET', `/api/v1/tmdb/search?${qs}`)
  },
  listLanguages: () => request<Language[]>('GET', '/api/v1/tmdb/languages'),
  tmdbGetMovie: (id: number) => request<unknown>('GET', `/api/v1/tmdb/movie/${id}`),
  tmdbGetTV: (id: number) => request<unknown>('GET', `/api/v1/tmdb/tv/${id}`),

  // Video link/unlink
  linkVideo: (videoId: number, mediaId: number, episodeId?: number) =>
    request<Video>('POST', `/api/v1/videos/${videoId}/link`, { media_id: mediaId, episode_id: episodeId }),
  unlinkVideo: (videoId: number) => request<void>('POST', `/api/v1/videos/${videoId}/unlink`),
  listUnlinkedVideos: (page?: number) => {
    const qs = new URLSearchParams()
    if (page) qs.set('page', String(page))
    return request<{ total: number; page: number; limit: number; videos: Video[] }>('GET', `/api/v1/videos/unlinked?${qs}`)
  },
}
