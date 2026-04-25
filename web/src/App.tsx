import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AuthProvider, useAuth } from './context/AuthContext'
import Login from './pages/Login'
import Library from './pages/Library'
import Watch from './pages/Watch'
import Admin from './pages/Admin'
import MediaDetail from './pages/MediaDetail'
import AdminMediaLink from './pages/AdminMediaLink'

const queryClient = new QueryClient()

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) return <div className="flex items-center justify-center h-screen text-white">Loading…</div>
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

function AdminRoute({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) return null
  if (!user || user.role !== 'admin') return <Navigate to="/" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <BrowserRouter>
          <div className="min-h-screen bg-gray-950 text-white">
            <Routes>
              <Route path="/login" element={<Login />} />
              <Route path="/" element={<ProtectedRoute><Library /></ProtectedRoute>} />
              <Route path="/watch/:id" element={<ProtectedRoute><Watch /></ProtectedRoute>} />
              <Route path="/media/:id" element={<ProtectedRoute><MediaDetail /></ProtectedRoute>} />
              <Route path="/admin/media/:id/link" element={<AdminRoute><AdminMediaLink /></AdminRoute>} />
              <Route path="/admin/*" element={<AdminRoute><Admin /></AdminRoute>} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </div>
        </BrowserRouter>
      </AuthProvider>
    </QueryClientProvider>
  )
}
