import { useEffect, useRef } from 'react'
import Plyr from 'plyr'
import Hls from 'hls.js'
import 'plyr/dist/plyr.css'
import type { Video } from '../api/client'

interface Props {
  video: Video
}

const PLYR_CONTROLS: string[] = [
  'play-large', 'play', 'rewind', 'fast-forward', 'progress',
  'current-time', 'duration', 'mute', 'volume', 'settings',
  'pip', 'fullscreen',
]

function formatClock(totalSec: number): string {
  const sec = Math.max(0, Math.floor(totalSec))
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}

export default function Player({ video }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const plyrRef = useRef<Plyr | null>(null)
  const hlsRef = useRef<Hls | null>(null)
  const durationHintRef = useRef<number | null>(video.duration_sec ?? null)

  useEffect(() => {
    durationHintRef.current = video.duration_sec ?? null
  }, [video.duration_sec, video.id])

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    // Tear down any previous instance.
    hlsRef.current?.destroy()
    hlsRef.current = null
    plyrRef.current?.destroy()
    plyrRef.current = null
    container.innerHTML = ''

    // Create a fresh <video> element so Plyr always starts clean.
    const el = document.createElement('video')
    el.playsInline = true
    el.className = 'w-full h-full'
    container.appendChild(el)

    const canNativeHLS = el.canPlayType('application/vnd.apple.mpegurl') !== ''
    const mime = (video.content_type ?? '').toLowerCase()
    const directExt = /\.(mp4|m4v|webm|ogv)$/i.test(video.filename ?? '')
    const directMime =
      mime.includes('video/mp4') ||
      mime.includes('application/mp4') ||
      mime.includes('video/webm') ||
      mime.includes('video/ogg')
    // Prefer direct playback for browser-native containers/codecs. This avoids
    // unnecessary ffmpeg/HLS overhead and bypasses flaky transcode startup for
    // files that can already be played natively.
    const preferDirect = directExt || directMime
    const useHLS = !preferDirect && !!video.hls_url && (Hls.isSupported() || canNativeHLS)

    if (useHLS && Hls.isSupported()) {
      // hls.js must be attached BEFORE Plyr wraps the element.
      const hls = new Hls({
        startLevel: -1,
        enableWorker: true,
        // Seek-jump restarts can take several seconds before the target
        // segment appears; avoid premature client timeout/stall.
        fragLoadingTimeOut: 30000,
        manifestLoadingTimeOut: 30000,
        levelLoadingTimeOut: 30000,
        xhrSetup: (xhr, url) => {
          xhr.addEventListener('readystatechange', () => {
            // Stream playlist responses can include server-provided total duration.
            if (xhr.readyState < 2 || !url.includes('.m3u8')) return
            const raw = xhr.getResponseHeader('X-Total-Duration')
            if (!raw) return
            const v = Number.parseFloat(raw)
            if (Number.isFinite(v) && v > 0) {
              durationHintRef.current = v
            }
          })
        },
      })
      hlsRef.current = hls
      hls.loadSource(video.hls_url)
      hls.attachMedia(el)
      hls.on(Hls.Events.LEVEL_UPDATED, () => {
        const hint = durationHintRef.current
        if (!hint || hint <= 0) return
        // Nudge MediaSource duration after hint arrives.
        const mediaSource = (hls as unknown as { mediaSource?: MediaSource }).mediaSource
        if (!mediaSource) return
        try {
          if (!Number.isFinite(mediaSource.duration) || mediaSource.duration < hint) {
            mediaSource.duration = hint
          }
        } catch {
          // Ignore transient InvalidStateError while SourceBuffers update.
        }
      })
      hls.on(Hls.Events.ERROR, (_e, data) => {
        if (data.fatal) {
          // Fall back to direct stream only for browser-native formats.
          // For MKV/HEVC/etc this causes long probe/range loops and still
          // won't play in most browsers.
          hls.destroy()
          hlsRef.current = null
          if (preferDirect) {
            el.src = video.stream_url
          }
        }
      })
    } else if (useHLS && canNativeHLS) {
      // Safari native HLS.
      el.src = video.hls_url
    } else {
      // Direct stream (mp4 / mkv / etc.) – set src before Plyr so it
      // finds it on the element and correctly initialises the provider.
      el.src = video.stream_url
    }

    // Initialise Plyr AFTER attaching the source so it detects it correctly.
    const player = new Plyr(el, {
      controls: PLYR_CONTROLS,
      settings: ['quality', 'speed'],
      ratio: '16:9',
    })
    plyrRef.current = player

    // ── Duration fix for live HLS manifests ───────────────────────────────
    // hls.js marks on-the-fly transcode playlists as live, so HTMLMediaElement
    // duration may stay Infinity / partial and Plyr shows growing time.
    // We override the duration getter on THIS video element only, using a
    // known duration hint when browser duration is not trustworthy.
    const usingHlsJs = useHLS && Hls.isSupported()
    const mediaDurationDesc = Object.getOwnPropertyDescriptor(HTMLMediaElement.prototype, 'duration')
    if (usingHlsJs && mediaDurationDesc?.get) {
      Object.defineProperty(el, 'duration', {
        configurable: true,
        get() {
          const base = mediaDurationDesc.get!.call(this) as number
          const hint = durationHintRef.current
          if (!hint || hint <= 0) return base
          // Force stable total duration in HLS mode.
          return hint
        },
      })
    }

    const syncDisplayedDuration = () => {
      if (!usingHlsJs) return
      const hint = durationHintRef.current
      if (!hint || hint <= 0) return
      const durEl = container.querySelector('.plyr__time--duration')
      if (durEl) durEl.textContent = formatClock(hint)
    }
    syncDisplayedDuration()

    // If duration is still unknown (older DB rows), poll the video endpoint
    // briefly and adopt duration once backend probe writes it.
    let durationPoller: ReturnType<typeof setInterval> | null = null
    if (usingHlsJs && (!durationHintRef.current || durationHintRef.current <= 0)) {
      durationPoller = setInterval(async () => {
        try {
          const res = await fetch(`/api/v1/videos/${video.id}`, { credentials: 'include' })
          if (!res.ok) return
          const body = await res.json() as { duration_sec?: number | null }
          if (body.duration_sec && body.duration_sec > 0) {
            durationHintRef.current = body.duration_sec
            syncDisplayedDuration()
            if (durationPoller) {
              clearInterval(durationPoller)
              durationPoller = null
            }
          }
        } catch {
          // keep polling
        }
      }, 3000)
    }

    // ── Keepalive for on-the-fly HLS sessions ──────────────────────────────
    // The server kills the ffmpeg process 45 s after the last keepalive.
    // We send one every 10 s while playing, and stop on pause / unmount.
    // Only on-the-fly streams need this; direct streams have no server process.
    const isOTF = video.hls_url?.startsWith('/hls/')
    let keepaliveTimer: ReturnType<typeof setInterval> | null = null

    const startKeepalive = () => {
      if (!isOTF || keepaliveTimer !== null) return
      // Send immediately so the session knows playback started, then repeat.
      fetch(`/hls/${video.id}/keepalive`, { method: 'POST', credentials: 'include' }).catch(() => {})
      keepaliveTimer = setInterval(() => {
        fetch(`/hls/${video.id}/keepalive`, { method: 'POST', credentials: 'include' }).catch(() => {})
      }, 10_000)
    }

    const stopKeepalive = () => {
      if (keepaliveTimer !== null) {
        clearInterval(keepaliveTimer)
        keepaliveTimer = null
      }
    }

    el.addEventListener('play',   startKeepalive)
    el.addEventListener('pause',  stopKeepalive)
    el.addEventListener('ended',  stopKeepalive)
    el.addEventListener('timeupdate', syncDisplayedDuration)
    el.addEventListener('loadedmetadata', syncDisplayedDuration)

    return () => {
      if (durationPoller) clearInterval(durationPoller)
      stopKeepalive()
      el.removeEventListener('play',  startKeepalive)
      el.removeEventListener('pause', stopKeepalive)
      el.removeEventListener('ended', stopKeepalive)
      el.removeEventListener('timeupdate', syncDisplayedDuration)
      el.removeEventListener('loadedmetadata', syncDisplayedDuration)
      hlsRef.current?.destroy()
      hlsRef.current = null
      plyrRef.current?.destroy()
      plyrRef.current = null
    }
  }, [video.id, video.hls_url, video.stream_url, video.content_type, video.filename])

  return <div ref={containerRef} className="w-full aspect-video max-h-[70vh] rounded-xl overflow-hidden bg-black" />
}
