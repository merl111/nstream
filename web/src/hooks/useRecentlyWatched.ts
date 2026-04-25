import { useCallback } from 'react'

export interface WatchedEntry {
  videoId: number
  title: string        // episode/video title shown in watch page
  mediaId?: number
  mediaTitle?: string
  mediaType?: 'movie' | 'tv'
  posterUrl?: string
  seasonNumber?: number
  episodeNumber?: number
  watchedAt: number    // Date.now()
}

const KEY = 'nstream:recently_watched'
const MAX = 20

function load(): WatchedEntry[] {
  try {
    return JSON.parse(localStorage.getItem(KEY) ?? '[]')
  } catch {
    return []
  }
}

function save(entries: WatchedEntry[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(entries))
  } catch { /* quota — ignore */ }
}

export function useRecentlyWatched() {
  const record = useCallback((entry: Omit<WatchedEntry, 'watchedAt'>) => {
    const entries = load().filter(e => e.videoId !== entry.videoId)
    entries.unshift({ ...entry, watchedAt: Date.now() })
    save(entries.slice(0, MAX))
    // Notify other tabs / components on the same page
    window.dispatchEvent(new Event('nstream:watched'))
  }, [])

  const getAll = useCallback((): WatchedEntry[] => load(), [])

  const clear = useCallback(() => { localStorage.removeItem(KEY) }, [])

  return { record, getAll, clear }
}
