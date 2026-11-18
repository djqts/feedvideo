import React, { useState, useEffect, useCallback, useRef } from 'react'
import { getUserFollowers, getUserFollowing, follow, unfollow, isFollowing } from '../api/user'
import { Avatar, AvatarFallback } from './ui/avatar'
import { Button } from './ui/button'
import { useFocusTrap } from '../hooks/useFocusTrap'

interface FollowDialogProps {
  open: boolean
  onClose: () => void
  mode: 'followers' | 'following'
  userId: string
}

interface UserItem {
  id: number
  isFollowing?: boolean
}

export default function FollowDialog({ open, onClose, mode, userId }: FollowDialogProps) {
  const [users, setUsers] = useState<UserItem[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [offset, setOffset] = useState(0)
  const pageSize = 20
  const dialogRef = useRef<HTMLDivElement>(null)
  const loadingRef = useRef(false)

  const fetchUsers = useCallback(
    async (currentOffset: number) => {
      if (!userId || loadingRef.current) return
      const uidNum = parseInt(userId)
      if (isNaN(uidNum)) return

      loadingRef.current = true
      setLoading(true)
      try {
        const fn = mode === 'followers' ? getUserFollowers : getUserFollowing
        const { data } = await fn(uidNum, currentOffset, pageSize)
        const ids: number[] = data.data?.user_ids || []
        const newUsers = ids.map((id) => ({ id }))

        if (currentOffset === 0) {
          setUsers(newUsers)
        } else {
          setUsers((prev) => [...prev, ...newUsers])
        }
        setHasMore(data.data?.has_more || false)
      } catch {
        // silently fail
      } finally {
        loadingRef.current = false
        setLoading(false)
      }
    },
    [userId, mode]
  )

  useEffect(() => {
    if (open) {
      setOffset(0)
      setUsers([])
      setHasMore(false)
      fetchUsers(0)
    }
  }, [open, mode, userId])

  const handleLoadMore = () => {
    const nextOffset = offset + pageSize
    setOffset(nextOffset)
    fetchUsers(nextOffset)
  }

  const handleToggleFollow = async (targetId: number) => {
    setUsers((prev) =>
      prev.map((u) =>
        u.id === targetId ? { ...u, isFollowing: !u.isFollowing } : u
      )
    )
    try {
      const { data: followingData } = await isFollowing(targetId)
      const currentlyFollowing = followingData.following || false
      if (currentlyFollowing) {
        await unfollow(targetId)
        setUsers((prev) =>
          prev.map((u) =>
            u.id === targetId ? { ...u, isFollowing: false } : u
          )
        )
      } else {
        await follow(targetId)
        setUsers((prev) =>
          prev.map((u) =>
            u.id === targetId ? { ...u, isFollowing: true } : u
          )
        )
      }
    } catch {
      // revert
    }
  }

  // Task 3.4: Focus trap (handles Tab cycling and Escape)
  useFocusTrap(open, dialogRef, undefined, onClose)

  if (!open) return null

  const title = mode === 'followers' ? '粉丝' : '关注'

  return (
    <div
      className="follow-dialog-overlay fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onClose}
    >
      <div
        ref={dialogRef}
        className="follow-dialog-content w-full max-w-sm mx-4 rounded-lg bg-white shadow-lg"
        role="dialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h2 className="text-base font-semibold">{title}</h2>
          <button
            className="text-muted-foreground hover:text-foreground"
            onClick={onClose}
            aria-label="Close"
          >
            ✕
          </button>
        </div>
        <div className="max-h-80 overflow-y-auto px-4 py-3 space-y-2">
          {users.length === 0 && !loading && (
            <p className="text-center text-sm text-muted-foreground py-6">
              {mode === 'followers' ? '暂无粉丝' : '暂无关注'}
            </p>
          )}
          {users.map((u) => (
            <div key={u.id} className="flex items-center gap-3 py-1">
              <Avatar className="h-8 w-8">
                <AvatarFallback>{String(u.id).slice(0, 2)}</AvatarFallback>
              </Avatar>
              <span className="flex-1 text-sm">User {u.id}</span>
              <Button
                variant={u.isFollowing ? 'outline' : 'default'}
                size="sm"
                onClick={() => handleToggleFollow(u.id)}
              >
                {u.isFollowing ? '已关注' : '关注'}
              </Button>
            </div>
          ))}
          {loading && (
            <p className="text-center text-sm text-muted-foreground py-2">加载中...</p>
          )}
          {hasMore && !loading && (
            <button
              className="w-full text-center text-sm text-primary hover:underline py-2"
              onClick={handleLoadMore}
            >
              加载更多
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
