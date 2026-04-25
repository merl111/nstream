import { Link } from 'react-router-dom'
import type { Video } from '../api/client'

function formatDuration(sec: number | null): string {
  if (sec == null) return ''
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}

function formatSize(bytes: number | null): string {
  if (bytes == null) return ''
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

interface Props {
  video: Video
}

export default function VideoCard({ video }: Props) {
  const title = video.title || video.filename
  const dur = formatDuration(video.duration_sec)
  const size = formatSize(video.size_bytes)

  return (
    <Link to={`/watch/${video.id}`} className="group block">
      <div className="relative aspect-video bg-gray-800 rounded-xl overflow-hidden mb-2 ring-1 ring-white/5 group-hover:ring-indigo-500 transition-all">
        <div className="absolute inset-0 flex items-center justify-center">
          <svg className="w-12 h-12 text-gray-600 group-hover:text-indigo-400 transition-colors" fill="currentColor" viewBox="0 0 24 24">
            <path d="M8 5v14l11-7z" />
          </svg>
        </div>
        {dur && (
          <span className="absolute bottom-2 right-2 bg-black/70 text-white text-xs px-1.5 py-0.5 rounded font-mono">
            {dur}
          </span>
        )}
        {video.transcode_status === 'done' && (
          <span className="absolute top-2 left-2 bg-indigo-600/80 text-white text-xs px-1.5 py-0.5 rounded">
            HLS
          </span>
        )}
      </div>
      <p className="text-sm font-medium text-white line-clamp-2 leading-tight">{title}</p>
      {size && <p className="text-xs text-gray-500 mt-0.5">{size}</p>}
    </Link>
  )
}
