import React, { useState, useCallback } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { usePolling } from '../hooks/usePolling'
import api from '../api/client'
import { Button } from './ui/button'

function NavLink({ to, children, className = '' }: { to: string; children: React.ReactNode; className?: string }) {
  const location = useLocation()
  const isActive = location.pathname === to || (to !== '/' && location.pathname.startsWith(to))
  return (
    <Link
      to={to}
      className={`text-sm font-medium transition-colors ${
        isActive ? 'text-primary' : 'text-muted-foreground hover:text-foreground'
      } ${className}`}
    >
      {children}
    </Link>
  )
}

export default function Navbar() {
  const { uid, role, logout } = useAuth()
  const [unread, setUnread] = useState(0)

  const fetchUnread = useCallback(() => {
    api.get('/v1/notifications/unread-count')
      .then((r) => setUnread(r.data?.data?.total ?? 0))
      .catch(() => {})
  }, [])

  usePolling(fetchUnread, 15000, !!uid)

  return (
    <nav className="fixed top-0 w-full bg-white/95 backdrop-blur border-b z-50 h-14 px-4 md:px-6 flex gap-4 md:gap-6 items-center shadow-sm">
      <Link to="/" className="font-bold text-base shrink-0 tracking-tight">FeedVideo</Link>
      <div className="flex gap-3 md:gap-5 items-center">
        <NavLink to="/">首页</NavLink>
        <NavLink to="/search">搜索</NavLink>
        {uid && (
          <>
            <NavLink to="/notifications" className="relative">
              通知
              {unread > 0 && (
                <span className="absolute -top-2 -right-4 min-w-[16px] h-4 px-1 rounded-full bg-red-500 text-white text-[10px] leading-4 text-center">
                  {unread > 99 ? '99+' : unread}
                </span>
              )}
            </NavLink>
            <NavLink to="/creator">创作</NavLink>
            <NavLink to="/publish">发布</NavLink>
            <NavLink to="/me">我的</NavLink>
          </>
        )}
        <NavLink to="/topics">话题</NavLink>
        {role === 'admin' && (
          <>
            <NavLink to="/admin/reports">举报</NavLink>
            <NavLink to="/admin/users">用户</NavLink>
            <NavLink to="/admin/stats">数据</NavLink>
          </>
        )}
      </div>
      <span className="flex-1" />
      {uid ? (
        <Button variant="ghost" size="sm" onClick={logout} className="text-muted-foreground">退出</Button>
      ) : (
        <Link to="/login"><Button size="sm" className="min-h-[36px] min-w-[44px]">登录</Button></Link>
      )}
    </nav>
  )
}
