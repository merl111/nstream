import { useState, useRef, useCallback, type DragEvent, type ChangeEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type Container } from '../api/client'

interface Props {
  onClose: () => void
}

type FileState =
  | { tag: 'idle' }
  | { tag: 'receiving'; pct: number }
  | { tag: 'received' }
  | { tag: 'queued'; jobId: string }
  | { tag: 'error'; message: string }

export default function UploadModal({ onClose }: Props) {
  const { data: containers = [] } = useQuery({
    queryKey: ['containers'],
    queryFn: api.listContainers,
  })

  const [containerID, setContainerID] = useState<number | ''>('')
  const [files, setFiles] = useState<File[]>([])
  const [states, setStates] = useState<Record<string, FileState>>({})
  const [uploading, setUploading] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  function addFiles(incoming: FileList | null) {
    if (!incoming) return
    const arr = Array.from(incoming).filter(f => !files.find(x => x.name === f.name))
    setFiles(prev => [...prev, ...arr])
  }

  function removeFile(name: string) {
    setFiles(prev => prev.filter(f => f.name !== name))
    setStates(s => { const n = { ...s }; delete n[name]; return n })
  }

  const setState = useCallback((name: string, s: FileState) => {
    setStates(prev => ({ ...prev, [name]: s }))
  }, [])

  const upload = useCallback(async () => {
    if (!containerID || files.length === 0) return
    setUploading(true)

    for (const file of files) {
      const cur = states[file.name]
      if (cur?.tag === 'queued') continue
      setState(file.name, { tag: 'receiving', pct: 0 })
      try {
        const jobId = await api.uploadVideo(
          Number(containerID),
          file,
          ev => {
            if (ev.phase === 'receiving' && ev.pct !== undefined) {
              setState(file.name, { tag: 'receiving', pct: ev.pct })
            } else if (ev.phase === 'received') {
              setState(file.name, { tag: 'received' })
            }
          },
        )
        setState(file.name, { tag: 'queued', jobId })
      } catch (err: unknown) {
        setState(file.name, { tag: 'error', message: err instanceof Error ? err.message : 'Upload failed' })
      }
    }

    setUploading(false)

    // Auto-close if every file is queued (no errors left).
    setStates(prev => {
      const allQueued = files.every(f => {
        const s = prev[f.name]
        return s?.tag === 'queued'
      })
      if (allQueued) {
        // slight delay so user sees the green state for a moment
        setTimeout(onClose, 800)
      }
      return prev
    })
  }, [containerID, files, states, setState, onClose])

  const allQueued = files.length > 0 && files.every(f => states[f.name]?.tag === 'queued')

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70"
      onClick={e => { if (e.target === e.currentTarget) onClose() }}
    >
      <div className="w-full max-w-lg bg-gray-900 rounded-2xl shadow-2xl p-6 space-y-5">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold text-white">Upload videos</h2>
          <button onClick={onClose} className="text-gray-500 hover:text-white text-xl leading-none">✕</button>
        </div>

        {/* Container selector */}
        <div>
          <label className="block text-sm text-gray-400 mb-1">Target container</label>
          <select
            value={containerID}
            onChange={e => setContainerID(e.target.value ? Number(e.target.value) : '')}
            className="w-full px-3 py-2 bg-gray-800 border border-gray-700 rounded-lg text-white text-sm focus:outline-none focus:border-indigo-500"
          >
            <option value="">— select container —</option>
            {containers.map((c: Container) => (
              <option key={c.id} value={c.id}>{c.name} ({c.cid.slice(0, 12)}…)</option>
            ))}
          </select>
          {containers.length === 0 && (
            <p className="text-xs text-yellow-500 mt-1">No containers yet — add one in Admin → Containers first.</p>
          )}
        </div>

        {/* Drop zone */}
        <div
          onDrop={e => { e.preventDefault(); addFiles(e.dataTransfer.files) }}
          onDragOver={(e: DragEvent) => e.preventDefault()}
          onClick={() => inputRef.current?.click()}
          className="border-2 border-dashed border-gray-700 hover:border-indigo-500 rounded-xl p-6 text-center cursor-pointer transition-colors"
        >
          <p className="text-gray-400 text-sm">Drop video files here or <span className="text-indigo-400">browse</span></p>
          <p className="text-gray-600 text-xs mt-1">mp4, mkv, webm, avi, mov, …</p>
          <input
            ref={inputRef}
            type="file"
            accept="video/*"
            multiple
            className="hidden"
            onChange={(e: ChangeEvent<HTMLInputElement>) => addFiles(e.target.files)}
          />
        </div>

        {/* File list */}
        {files.length > 0 && (
          <div className="space-y-2 max-h-48 overflow-y-auto">
            {files.map(f => {
              const s = states[f.name] ?? { tag: 'idle' }
              return (
                <div key={f.name} className="bg-gray-800 rounded-lg px-3 py-2">
                  <div className="flex items-center gap-2 min-w-0">
                    <span className="flex-1 text-sm text-white truncate min-w-0">{f.name}</span>
                    <span className="text-xs text-gray-500 shrink-0">{(f.size / 1024 / 1024).toFixed(1)} MB</span>

                    {s.tag === 'receiving' && (
                      <span className="text-xs text-indigo-400 shrink-0">Uploading {s.pct}%</span>
                    )}
                    {s.tag === 'received' && (
                      <span className="text-xs text-indigo-300 shrink-0">Queuing…</span>
                    )}
                    {s.tag === 'queued' && (
                      <span className="text-xs text-green-400 shrink-0">✓ Queued</span>
                    )}
                    {s.tag === 'error' && (
                      <span className="text-xs text-red-400 shrink-0">Error</span>
                    )}
                    {s.tag === 'idle' && !uploading && (
                      <button onClick={() => removeFile(f.name)} className="text-gray-600 hover:text-red-400 text-sm leading-none shrink-0">✕</button>
                    )}
                  </div>

                  {s.tag === 'receiving' && (
                    <div className="mt-1.5 h-1 bg-gray-700 rounded-full overflow-hidden">
                      <div
                        className="h-full bg-indigo-500 rounded-full transition-all duration-300"
                        style={{ width: `${s.pct}%` }}
                      />
                    </div>
                  )}
                  {s.tag === 'received' && (
                    <div className="mt-1.5 h-1 bg-gray-700 rounded-full overflow-hidden">
                      <div className="h-full w-full bg-indigo-400 rounded-full animate-pulse" />
                    </div>
                  )}
                  {s.tag === 'error' && (
                    <p className="text-red-400 text-xs mt-1">{s.message}</p>
                  )}
                  {s.tag === 'queued' && (
                    <p className="text-gray-500 text-xs mt-0.5">NeoFS upload running in background — you can close this.</p>
                  )}
                </div>
              )
            })}
          </div>
        )}

        {allQueued && (
          <p className="text-sm text-green-400 text-center">
            All files queued. Track progress in the upload indicator above the library.
          </p>
        )}

        {/* Actions */}
        <div className="flex gap-3 justify-end">
          <button onClick={onClose} className="px-4 py-2 text-sm text-gray-400 hover:text-white transition-colors">
            {allQueued ? 'Close' : uploading ? 'Close (continues in background)' : 'Cancel'}
          </button>
          {!allQueued && (
            <button
              onClick={upload}
              disabled={uploading || !containerID || files.length === 0}
              className="px-5 py-2 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 rounded-lg text-sm font-medium transition-colors"
            >
              {uploading
                ? `Uploading…`
                : `Upload${files.length > 1 ? ` ${files.length} files` : ''}`}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
