import React, { useState, useRef } from 'react'
import { searchUsers } from '../api/search'
import { banUser, muteUser } from '../api/moderation'
import { verifyUser } from '../api/creator'
import { Card, CardContent } from '../components/ui/card'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import DangerConfirm from '../components/DangerConfirm'

export default function AdminUsersPage() {
  const [query, setQuery] = useState('')
  const [users, setUsers] = useState<any[]>([])
  const [loading, setLoading] = useState(false)
  const [searched, setSearched] = useState(false)

  const handleSearch = async () => {
    if (!query.trim()) return
    setLoading(true)
    setSearched(true)
    try {
      const r = await searchUsers(query.trim())
      setUsers(r.data?.data?.users || r.data?.data || [])
    } catch {
      setUsers([])
    } finally {
      setLoading(false)
    }
  }

  const handleBan = async (uid: string, ban: boolean) => {
    try {
      await banUser(uid, ban)
      setUsers((prev) =>
        prev.map((u: any) => (String(u.id || u.uid) === uid ? { ...u, banned: ban } : u)),
      )
    } catch {}
  }

  const handleMute = async (uid: string, m: boolean) => {
    try {
      await muteUser(uid, m)
      setUsers((prev) =>
        prev.map((u: any) => (String(u.id || u.uid) === uid ? { ...u, muted: m } : u)),
      )
    } catch {}
  }

  const handleVerify = async (uid: string) => {
    try {
      await verifyUser(uid, true)
      setUsers((prev) =>
        prev.map((u: any) => (String(u.id || u.uid) === uid ? { ...u, verified: true } : u)),
      )
    } catch {}
  }

  return (
    <div>
      <h1 className="text-lg font-bold mb-4">用户管理</h1>

      <div className="flex gap-2 mb-4">
        <Input
          className="h-11 text-base"
          placeholder="搜索用户昵称或 UID..."
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') handleSearch() }}
        />
        <Button onClick={handleSearch} disabled={loading} className="min-h-[44px]">
          {loading ? '搜索中...' : '搜索'}
        </Button>
      </div>

      {searched && users.length === 0 && !loading && (
        <p className="text-center text-muted-foreground mt-16">
          {query ? `没有找到"${query}"相关用户` : '请输入搜索关键词'}
        </p>
      )}

      {loading && (
        <p className="text-center text-muted-foreground mt-16">搜索中...</p>
      )}

      {users.length > 0 && (
        <div className="space-y-2">
          {users.map((u: any) => {
            const userId = String(u.id || u.uid)
            const nickname = u.nickname || `用户 ${userId}`
            const isBanned = u.banned || false
            const isMuted = u.muted || false

            return (
              <Card key={userId}>
                <CardContent className="p-3 flex items-center justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="size-10 rounded-full bg-muted flex items-center justify-center text-sm font-semibold shrink-0">
                      {nickname.slice(0, 2)}
                    </div>
                    <div className="min-w-0">
                      <div className="text-sm font-semibold truncate">{nickname}</div>
                      <div className="text-xs text-muted-foreground">UID: {userId}</div>
                      {u.bio && <div className="text-xs text-muted-foreground line-clamp-1">{u.bio}</div>}
                    </div>
                  </div>
                  <div className="flex gap-2 shrink-0">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => handleVerify(userId)}
                      data-testid={`verify-${userId}`}
                    >
                      {u.verified ? '已认证' : '认证创作者'}
                    </Button>
                    <DangerConfirm
                      title={isBanned ? '解除封禁' : '封禁用户'}
                      description={`确认${isBanned ? '解除封禁' : '封禁'}用户 **${nickname}**？`}
                      confirmText={isBanned ? '解封' : '封禁'}
                      consequenceText={isBanned ? '' : '封禁后该用户将无法登录和使用平台'}
                      requireCountdown={!isBanned}
                      onConfirm={() => handleBan(userId, !isBanned)}
                    >
                      <Button variant={isBanned ? 'outline' : 'destructive'} size="sm">
                        {isBanned ? '已封禁' : '封禁'}
                      </Button>
                    </DangerConfirm>
                    <DangerConfirm
                      title={isMuted ? '解除禁言' : '禁言用户'}
                      description={`确认${isMuted ? '解除禁言' : '禁言'}用户 **${nickname}**？`}
                      confirmText={isMuted ? '解禁' : '禁言'}
                      consequenceText={isMuted ? '' : '禁言后该用户将无法发布评论和视频'}
                      onConfirm={() => handleMute(userId, !isMuted)}
                    >
                      <Button variant={isMuted ? 'outline' : 'outline'} size="sm">
                        {isMuted ? '已禁言' : '禁言'}
                      </Button>
                    </DangerConfirm>
                  </div>
                </CardContent>
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}
