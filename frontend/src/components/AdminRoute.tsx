import React, { useCallback } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import ForbiddenPage from '../pages/ForbiddenPage'
import AccountDisabledPage from '../pages/AccountDisabledPage'
import Spinner from './Spinner'
import Banner from './Banner'
import { Button } from './ui/button'

export default function AdminRoute({ children }: { children: React.ReactNode }) {
  const { snapshot, retry } = useAuth()
  const { status, availability } = snapshot

  switch (status.kind) {
    case 'initializing':
      return <Spinner />

    case 'authenticated':
      if (status.role !== 'admin') {
        return <ForbiddenPage />
      }
      if (availability === 'temporarily-unavailable') {
        return (
          <>
            <Banner>认证服务暂时不可用，管理操作已禁用</Banner>
            {children}
          </>
        )
      }
      return <>{children}</>

    case 'anonymous':
      return <Navigate to={`/login?redirect=${encodeURIComponent(window.location.pathname)}`} replace />

    case 'unknown':
      return (
        <div className="text-center mt-24">
          <h1 className="text-2xl font-bold text-muted-foreground mb-3">认证服务不可用</h1>
          <p className="text-sm text-muted-foreground mb-6">无法验证您的身份，请稍后重试</p>
          <Button onClick={retry}>重试</Button>
        </div>
      )

    case 'account-disabled':
      return <AccountDisabledPage />

    default:
      return <Spinner />
  }
}
