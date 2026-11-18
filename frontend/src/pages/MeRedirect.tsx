import React from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

/**
 * /me → /profile/:uid 重定向。
 * ProtectedRoute 已处理未登录情况，渲染到这里时 uid 应当存在。
 */
export default function MeRedirect() {
  const { uid } = useAuth()
  if (!uid) return null
  return <Navigate to={`/profile/${uid}`} replace />
}
