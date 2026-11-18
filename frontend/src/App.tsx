import React, { Suspense } from 'react'
import { Routes, Route } from 'react-router-dom'
import { Toaster } from 'sonner'
import { AuthProvider } from './context/AuthContext'
import ProtectedRoute from './components/ProtectedRoute'
import AdminRoute from './components/AdminRoute'
import Navbar from './components/Navbar'
import MobileNav from './components/MobileNav'
import SlowNetworkBar from './components/SlowNetworkBar'
import Spinner from './components/Spinner'

// ---------------------------------------------------------------------------
// Route lazy loading — all page components are code-split for smaller initial
// bundle size (Task 10.1).
// ---------------------------------------------------------------------------
const LoginPage = React.lazy(() => import('./pages/LoginPage'))
const FeedPage = React.lazy(() => import('./pages/FeedPage'))
const SearchPage = React.lazy(() => import('./pages/SearchPage'))
const NotificationPage = React.lazy(() => import('./pages/NotificationPage'))
const ProfilePage = React.lazy(() => import('./pages/ProfilePage'))
const MeRedirect = React.lazy(() => import('./pages/MeRedirect'))
const CreatorPage = React.lazy(() => import('./pages/CreatorPage'))
const TopicPlaza = React.lazy(() => import('./pages/TopicPlaza'))
const TopicPage = React.lazy(() => import('./pages/TopicPage'))
const AdminLayout = React.lazy(() => import('./pages/AdminLayout'))
const AdminReportsPage = React.lazy(() => import('./pages/AdminReportsPage'))
const AdminUsersPage = React.lazy(() => import('./pages/AdminUsersPage'))
const AdminStatsPage = React.lazy(() => import('./pages/AdminStatsPage'))
const AdminVideosPage = React.lazy(() => import('./pages/AdminVideosPage'))
const HistoryPage = React.lazy(() => import('./pages/HistoryPage'))
const PublishPage = React.lazy(() => import('./pages/PublishPage'))
const CenteredLayout = React.lazy(() => import('./pages/CenteredLayout'))
const VideoPage = React.lazy(() => import('./pages/VideoPage'))
const CollectionsPage = React.lazy(() => import('./pages/CollectionsPage'))
const BlacklistPage = React.lazy(() => import('./pages/BlacklistPage'))
const FriendsPage = React.lazy(() => import('./pages/FriendsPage'))
const NotFoundPage = React.lazy(() => import('./pages/NotFoundPage'))

const SuspenseFallback = (
  <div className="flex items-center justify-center min-h-screen">
    <Spinner />
  </div>
)

export default function App() {
  return (
    <AuthProvider>
      <a
        href="#main-content"
        className="ef-skip"
      >
        跳到内容
      </a>
      <Toaster position="top-center" closeButton toastOptions={{ className: 'ef-toast' }} offset={88} />
      <Navbar />
      <Suspense fallback={SuspenseFallback}>
        <Routes>
          <Route path="/admin" element={<AdminRoute><AdminLayout /></AdminRoute>}>
            <Route path="reports" element={<AdminReportsPage />} />
            <Route path="videos" element={<AdminVideosPage />} />
            <Route path="users" element={<AdminUsersPage />} />
            <Route path="stats" element={<AdminStatsPage />} />
          </Route>
          <Route element={<CenteredLayout />}>
            <Route path="/login" element={<LoginPage />} />
            <Route path="/" element={<ProtectedRoute><FeedPage /></ProtectedRoute>} />
            <Route path="/videos/:id" element={<VideoPage />} />
            <Route path="/search" element={<SearchPage />} />
            <Route path="/notifications" element={<ProtectedRoute><NotificationPage /></ProtectedRoute>} />
            <Route path="/me" element={<ProtectedRoute><MeRedirect /></ProtectedRoute>} />
            <Route path="/profile/:uid" element={<ProfilePage />} />
            <Route path="/creator" element={<ProtectedRoute><CreatorPage /></ProtectedRoute>} />
            <Route path="/publish" element={<ProtectedRoute><PublishPage /></ProtectedRoute>} />
            <Route path="/topics" element={<TopicPlaza />} />
            <Route path="/topics/id/:topicId" element={<TopicPage />} />
            <Route path="/topics/:name" element={<TopicPage />} />
            <Route path="/collections" element={<ProtectedRoute><CollectionsPage /></ProtectedRoute>} />
            <Route path="/settings/blacklist" element={<ProtectedRoute><BlacklistPage /></ProtectedRoute>} />
            <Route path="/friends" element={<ProtectedRoute><FriendsPage /></ProtectedRoute>} />
            <Route path="/history" element={<ProtectedRoute><HistoryPage /></ProtectedRoute>} />
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Routes>
      </Suspense>
      <MobileNav />
      <SlowNetworkBar />
    </AuthProvider>
  )
}
