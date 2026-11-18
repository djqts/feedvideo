import React from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { Button } from './ui/button'

export default function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { status, availability, retry } = useAuth()
  const location = useLocation()

  switch (status.kind) {
    case 'initializing':
      return <div className="flex items-center justify-center min-h-[60vh]">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" />
      </div>

    case 'authenticated':
      // Keep the page even when availability is temporarily-unavailable.
      // Design §5.7: "authenticated + temporarily-unavailable → 保留现有页面不跳转"
      return <>{children}</>

    case 'anonymous':
      // Design §5.7: anonymous → /login?redirect=... regardless of availability
      return (
        <Navigate
          to={`/login?redirect=${encodeURIComponent(
            location.pathname + location.search + location.hash,
          )}`}
          replace
        />
      )

    case 'unknown':
      // Design §5.7: unknown → "认证服务不可用" + retry, NOT redirect to /login.
      // Only when there is no confirmed identity in this SPA lifecycle
      // and the auth backend is temporarily unreachable.
      return (
        <div className="flex flex-col items-center justify-center min-h-[60vh] gap-4 px-4">
          <div className="text-lg font-semibold text-muted-foreground">
            认证服务暂时不可用
          </div>
          <p className="text-sm text-muted-foreground text-center max-w-md">
            无法确认您的登录状态，请检查网络连接后重试。
          </p>
          <Button onClick={() => retry()} variant="default">
            重试
          </Button>
        </div>
      )

    case 'account-disabled':
      // Design §5.7: account-disabled → display dedicated message, no redirect
      return (
        <div className="flex flex-col items-center justify-center min-h-[60vh] gap-4 px-4">
          <div className="text-lg font-semibold text-destructive">
            账户已被禁用
          </div>
          <p className="text-sm text-muted-foreground text-center max-w-md">
            您的账户已被管理员禁用。如有疑问，请联系管理员。
          </p>
          <Button onClick={() => (window.location.href = '/')} variant="outline">
            返回首页
          </Button>
        </div>
      )

    default:
      // Exhaustive check — should never reach here
      return null
  }
}
