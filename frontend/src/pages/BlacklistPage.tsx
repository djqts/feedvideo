import React, { useState, useEffect, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { getBlacklist, unblackUser } from '../api/blacklist'
import { Button } from '../components/ui/button'
import { Card, CardContent } from '../components/ui/card'
import { ListSkeleton } from '../components/skeletons/ListSkeleton'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'

export default function BlacklistPage() {
  const [users, setUsers] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const { data } = await getBlacklist(1, 50)
      const ids = data.data?.user_ids
      setUsers(Array.isArray(ids) ? ids.map((id: number) => ({ id })) : [])
    } catch (err) {
      setError(classifyError(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const handleUnblack = async (uid: number) => {
    try {
      await unblackUser(uid)
      setUsers((prev) => prev.filter((u: any) => u.id !== uid))
    } catch {}
  }

  if (loading) return <div className="max-w-md mx-auto mt-8"><ListSkeleton n={5} /></div>
  if (error) return <div className="max-w-md mx-auto mt-8"><ErrorState error={error} onRetry={async () => { await load() }} /></div>

  return (
    <div className="max-w-md mx-auto">
      <h1 className="text-lg font-bold mb-4">黑名单</h1>
      {users.length === 0 ? (
        <p className="text-center text-muted-foreground mt-16">黑名单中没有用户。</p>
      ) : (
        <div className="space-y-2">
          {users.map((u: any) => (
            <Card key={u.id}>
              <CardContent className="p-3 flex items-center justify-between">
                <Link to={`/profile/${u.id}`} className="flex items-center gap-3 flex-1 min-w-0 hover:underline">
                  <div className="size-10 rounded-full bg-muted flex items-center justify-center text-sm font-semibold shrink-0">
                    {String(u.id).slice(0, 2)}
                  </div>
                  <span className="text-sm font-medium">用户 {u.id}</span>
                </Link>
                <Button variant="outline" size="sm" onClick={() => handleUnblack(u.id)}>
                  解除拉黑
                </Button>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
