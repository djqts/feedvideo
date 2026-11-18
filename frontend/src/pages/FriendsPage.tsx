import React, { useState, useEffect, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { getFriends } from '../api/friends'
import { Card, CardContent } from '../components/ui/card'
import { ListSkeleton } from '../components/skeletons/ListSkeleton'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'

export default function FriendsPage() {
  const [friends, setFriends] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const { data } = await getFriends(1, 50)
      const ids = data.data?.user_ids
      setFriends(Array.isArray(ids) ? ids.map((id: number) => ({ id })) : [])
    } catch (err) {
      setError(classifyError(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  if (loading) return <div className="max-w-md mx-auto mt-8"><ListSkeleton n={5} /></div>
  if (error) return <div className="max-w-md mx-auto mt-8"><ErrorState error={error} onRetry={async () => { await load() }} /></div>

  return (
    <div className="max-w-md mx-auto">
      <h1 className="text-lg font-bold mb-4">好友</h1>
      {friends.length === 0 ? (
        <div className="text-center mt-16">
          <p className="text-muted-foreground">暂无互关好友。</p>
          <p className="text-sm text-muted-foreground mt-2">
            关注一些用户后，互相关注的好友会显示在这里
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {friends.map((u: any) => (
            <Link key={u.id} to={`/profile/${u.id}`} className="block">
              <Card className="hover:bg-accent/50 transition-colors">
                <CardContent className="p-3 flex items-center gap-3">
                  <div className="size-10 rounded-full bg-muted flex items-center justify-center text-sm font-semibold shrink-0">
                    {String(u.id).slice(0, 2)}
                  </div>
                  <div>
                    <div className="text-sm font-semibold">用户 {u.id}</div>
                  </div>
                </CardContent>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
