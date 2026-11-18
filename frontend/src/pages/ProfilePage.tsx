import React, { useState, useEffect, useCallback, useRef } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import PageHeader from '../components/PageHeader'
import { useFocusTrap } from '../hooks/useFocusTrap'
import { follow, unfollow, isFollowing, getUserFollowers, getUserFollowing } from '../api/user'
import { getProfile, updateProfile } from '../api/profile'
import FollowDialog from '../components/FollowDialog'
import { ListSkeleton } from '../components/skeletons/ListSkeleton'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import ErrorState from '../components/ErrorState'
import { blackUser } from '../api/blacklist'
import { getCommonFollowing } from '../api/friends'
import { toast } from 'sonner'

const PROFILE_TABS = ['works', 'followers', 'following'] as const
const profileTabLabels: Record<string, string> = { works: '作品', followers: '粉丝', following: '关注' }

export default function ProfilePage() {
  const { uid } = useParams()
  const { uid: myUid, snapshot } = useAuth()
  const navigate = useNavigate()
  const isOwn = String(myUid) === uid

  // Profile info
  const [profile, setProfile] = useState<any>(null)
  const [profileLoading, setProfileLoading] = useState(true)

  // Relation
  const [following, setFollowing] = useState(false)
  const [followerCount, setFollowerCount] = useState(0)
  const [followingCount, setFollowingCount] = useState(0)
  const [commonCount, setCommonCount] = useState(0)
  const [blocking, setBlocking] = useState(false)

  // Tab state
  const [tab, setTab] = useState<string>('works')
  const [users, setUsers] = useState<any[]>([])
  const [works, setWorks] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ClassifiedError | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [dialogMode, setDialogMode] = useState<'followers' | 'following'>('followers')

  // Edit state
  const [editing, setEditing] = useState(false)
  const [editNickname, setEditNickname] = useState('')
  const [editBio, setEditBio] = useState('')
  const [editAvatar, setEditAvatar] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const savingRef = useRef(false)
  const editDialogRef = useRef<HTMLDivElement>(null)
  const editTriggerRef = useRef<HTMLElement | null>(null)
  const closeEditing = useCallback(() => {
    if (!savingRef.current) setEditing(false)
  }, [])
  useFocusTrap(editing, editDialogRef, editTriggerRef, closeEditing)

  const openDialog = (mode: 'followers' | 'following') => {
    setDialogMode(mode)
    setDialogOpen(true)
  }

  // Load profile info
  const loadProfile = useCallback(async () => {
    if (!uid) return
    setProfile(null)
    setProfileLoading(true)
    if (!isOwn) {
      setProfileLoading(false)
      return
    }
    try {
      const { data } = await getProfile()
      const record = data.profile || data.data || data
      setProfile({ ...record, nickname: record.nickname ?? record.username, avatar_url: record.avatar_url ?? record.avatar })
    } catch {
      // Public profile fallback — just show uid
      setProfile(null)
    } finally {
      setProfileLoading(false)
    }
  }, [uid, isOwn])

  // Load relation counts
  const loadRelation = useCallback(async () => {
    if (!uid) return
    const uidNum = parseInt(uid)
    if (isNaN(uidNum)) return

    if (myUid && !isOwn) {
      try {
        const { data } = await isFollowing(uidNum)
        setFollowing(data.following || false)
      } catch { setFollowing(false) }
    } else { setFollowing(false) }
    try {
      const [fr, fg] = await Promise.all([
        getUserFollowers(uidNum, 0, 1),
        getUserFollowing(uidNum, 0, 1),
      ])
      setFollowerCount(fr.data?.data?.total ?? fr.data?.data?.user_ids?.length ?? 0)
      setFollowingCount(fg.data?.data?.total ?? fg.data?.data?.user_ids?.length ?? 0)
    } catch {}
    if (myUid && !isOwn) {
      try {
        const { data } = await getCommonFollowing(uidNum)
        const ids = data?.data?.user_ids ?? data?.data ?? []
        setCommonCount(Array.isArray(ids) ? ids.length : 0)
      } catch { setCommonCount(0) }
    } else {
      setCommonCount(0)
    }
  }, [uid, isOwn, myUid])

  // Load tab content
  const loadTabContent = useCallback(async () => {
    if (!uid) return
    const uidNum = parseInt(uid)
    if (isNaN(uidNum)) return
    setLoading(true)
    setError(null)

    try {
      if (tab === 'works') {
        const r = await import('../api/client').then(m => m.default.get('/v1/videos/search', { params: { author_id: uid, page: 1, page_size: 20 } }))
        const payload = r.data?.data
        const entries = payload?.list ?? payload?.videos ?? payload ?? []
        const videos = Array.isArray(entries) ? entries.map((entry: any) => entry.video ? { ...entry.video, stats: entry.stats } : entry) : []
        setWorks(videos.filter((v: any) => String(v.author_id ?? v.user_id ?? v.owner_id) === uid))
      } else {
        const fn = tab === 'followers' ? getUserFollowers : getUserFollowing
        const { data } = await fn(uidNum, 0, 50)
        setUsers(data.data?.user_ids?.map((id: number) => ({ id })) || [])
      }
    } catch (err) {
      setError(classifyError(err))
    } finally {
      setLoading(false)
    }
  }, [uid, tab, snapshot])

  useEffect(() => { loadProfile() }, [loadProfile])
  useEffect(() => { loadRelation() }, [loadRelation])
  useEffect(() => { loadTabContent() }, [loadTabContent])

  const handleFollow = async () => {
    if (!uid) return
    if (!myUid) { navigate('/login'); return }
    const uidNum = parseInt(uid)
    if (isNaN(uidNum)) return
    setFollowing(!following)
    setFollowerCount(c => Math.max(0, following ? c - 1 : c + 1))
    try {
      if (following) await unfollow(uidNum)
      else await follow(uidNum)
    } catch {
      setFollowing(following)
      setFollowerCount(c => Math.max(0, following ? c + 1 : c - 1))
    }
  }

  const handleBlock = async () => {
    if (!uid || blocking) return
    const target = Number(uid)
    if (!Number.isFinite(target)) return
    setBlocking(true)
    try {
      await blackUser(target)
      setFollowing(false)
      setFollowerCount(c => Math.max(0, c - 1))
      toast.success('已加入黑名单')
    } catch {
      toast.error('拉黑失败，请稍后再试')
    } finally {
      setBlocking(false)
    }
  }

  const handleSaveProfile = async () => {
    if (savingRef.current) return
    savingRef.current = true
    setSaving(true)
    setSaveError('')
    try {
      await updateProfile({
        nickname: editNickname,
        bio: editBio,
        avatar_url: editAvatar,
      })
      const { data } = await getProfile()
      const record = data.profile || data.data || data
      setProfile({ ...record, nickname: record.nickname ?? record.username, avatar_url: record.avatar_url ?? record.avatar })
      setEditing(false)
    } catch (err: any) {
      setSaveError(err?.response?.data?.message || err?.response?.data?.error || err?.message || '保存失败，请稍后重试')
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  }

  const startEditing = () => {
    editTriggerRef.current = document.activeElement as HTMLElement
    setSaveError('')
    setEditNickname(profile?.nickname || '')
    setEditBio(profile?.bio || '')
    setEditAvatar(profile?.avatar_url || '')
    setEditing(true)
  }

  // Keyboard nav for tabs
  const handleTabKeyDown = (e: React.KeyboardEvent, currentIndex: number) => {
    let nextIndex = currentIndex
    if (e.key === 'ArrowRight') {
      nextIndex = (currentIndex + 1) % PROFILE_TABS.length
    } else if (e.key === 'ArrowLeft') {
      nextIndex = (currentIndex - 1 + PROFILE_TABS.length) % PROFILE_TABS.length
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      setTab(PROFILE_TABS[currentIndex])
      return
    } else {
      return
    }
    e.preventDefault()
    const tabs = e.currentTarget.parentElement?.querySelectorAll<HTMLElement>('[role="tab"]')
    tabs?.[nextIndex]?.focus()
  }

  const displayName = profile?.nickname || `UID: ${uid}`
  const displayBio = profile?.bio || ''
  const avatarUrl = profile?.avatar_url || ''

  return (
    <div className="mx-auto max-w-6xl">
      <section className="border-b border-border pb-8" aria-label="个人资料" aria-busy={profileLoading}>
        <div className="flex flex-wrap items-start gap-5 sm:gap-8">
          <div className="flex h-20 w-20 shrink-0 items-center justify-center border border-border bg-muted text-2xl font-bold sm:h-28 sm:w-28">
            {avatarUrl ? <img src={avatarUrl} alt={displayName} className="h-full w-full object-cover" /> : <span aria-hidden="true">{(displayName || String(uid)).slice(0, 2)}</span>}
          </div>
          <div className="min-w-0 flex-1">
            <PageHeader eyebrow="PROFILE" title={displayName} description={displayBio || (isOwn ? '添加个人简介，让更多人了解你。' : undefined)} className="mb-0 border-0 pb-0 [&_h1]:break-words [&_p]:break-words" />
            <p className="ef-number text-sm mt-4 text-muted-foreground">UID / {uid}</p>
          </div>
          <div className="w-full sm:w-auto flex flex-wrap gap-2">
            {isOwn ? <Button variant="outline" onClick={startEditing} disabled={profileLoading}>编辑资料</Button> : (
              <>
                <Button variant={following ? 'outline' : 'default'} onClick={handleFollow} aria-pressed={following}>{following ? '已关注' : '关注'}</Button>
                <Button variant="outline" onClick={handleBlock} disabled={blocking}>{blocking ? '处理中...' : '拉黑'}</Button>
              </>
            )}
          </div>
        </div>
        <div className="mt-7 flex flex-wrap gap-3">
          <button type="button" aria-label={followerCount + ' 粉丝'} onClick={() => openDialog('followers')} className="ef-cut flex min-h-12 items-baseline gap-3 bg-muted px-5 py-3 text-sm hover:bg-accent hover:text-accent-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"><span className="ef-number text-2xl font-bold">{followerCount}</span><span>粉丝</span></button>
          <button type="button" aria-label={followingCount + ' 关注'} onClick={() => openDialog('following')} className="ef-cut flex min-h-12 items-baseline gap-3 bg-muted px-5 py-3 text-sm hover:bg-accent hover:text-accent-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"><span className="ef-number text-2xl font-bold">{followingCount}</span><span>关注</span></button>
          {!isOwn && commonCount > 0 && (
            <span className="ef-cut inline-flex min-h-12 items-center bg-muted px-5 py-3 text-sm text-muted-foreground">共同关注 <span className="ef-number mx-1 font-bold text-foreground">{commonCount}</span> 人</span>
          )}
          {!isOwn && commonCount > 0 && (
            <span className="ef-cut inline-flex min-h-12 items-center bg-muted px-5 py-3 text-sm text-muted-foreground">共同关注 <span className="ef-number mx-1 font-bold text-foreground">{commonCount}</span> 人</span>
          )}
        </div>
      </section>

      <div role="tablist" aria-label="资料分类" className="my-6 flex flex-wrap gap-2">
        {PROFILE_TABS.map((t, i) => (
          <button key={t} id={'profile-tab-' + t} type="button" role="tab" aria-selected={tab === t} aria-controls="profile-content" tabIndex={tab === t ? 0 : -1} onClick={() => setTab(t)} onKeyDown={(e) => handleTabKeyDown(e, i)} className="ef-tab">
            {profileTabLabels[t] || t}
          </button>
        ))}
      </div>

      <div id="profile-content" role="tabpanel" aria-labelledby={'profile-tab-' + tab} aria-busy={loading} className="min-w-0">
        {loading ? <ListSkeleton n={4} /> : error ? <ErrorState error={error} onRetry={async () => { await loadTabContent() }} /> : tab === 'works' ? (
          works.length > 0 ? (
            <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
              {works.map((v: any) => (
                <Link key={v.id} to={'/videos/' + v.id} className="group min-w-0 border-b border-border pb-4 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-foreground">
                  <div className="aspect-video overflow-hidden bg-muted">
                    {v.cover_url ? <img src={v.cover_url} alt="" loading="lazy" className="h-full w-full object-cover" /> : <div className="flex h-full items-center justify-center"><svg aria-hidden="true" className="h-10 w-10 text-muted-foreground" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z" /></svg></div>}
                  </div>
                  <h2 className="mt-4 break-words text-base font-semibold leading-7 group-hover:underline">{v.title || '未命名视频'}</h2>
                  <p className="ef-number text-sm mt-2 text-muted-foreground">{v.stats?.play_count || 0} 次播放</p>
                </Link>
              ))}
            </div>
          ) : <div className="ef-empty px-6 py-14 text-center"><p className="text-sm leading-7">{isOwn ? '还没有发布作品。发布的视频会出现在这里。' : '这位用户还没有发布作品。'}</p>{isOwn && <Link to="/publish" className="ef-cut mt-5 inline-flex min-h-12 items-center bg-primary px-6 py-3 text-sm font-semibold text-primary-foreground hover:bg-accent hover:text-accent-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground">发布视频</Link>}</div>
        ) : users.length > 0 ? (
          <div className="grid gap-x-10 sm:grid-cols-2">
            {users.map((u: any, index) => (
              <Link key={u.id} to={'/profile/' + u.id} className="ef-record flex min-w-0 items-center gap-4 py-4">
                <span className="ef-number w-7 shrink-0 text-xs text-muted-foreground">{String(index + 1).padStart(2, '0')}</span>
                <div className="flex h-12 w-12 shrink-0 items-center justify-center bg-muted text-sm font-semibold">{String(u.id).slice(0, 2)}</div>
                <div className="min-w-0"><span className="block break-words text-sm font-semibold">用户 {u.id}</span><span className="ef-number text-sm text-muted-foreground">UID / {u.id}</span></div>
              </Link>
            ))}
          </div>
        ) : <div className="ef-empty px-6 py-14 text-center"><p className="text-sm leading-7">{tab === 'followers' ? '该用户还没有粉丝。关注这个账号的用户会出现在这里。' : '该用户还没有关注任何人。关注的账号会出现在这里。'}</p></div>}
      </div>

      {editing && (
        <div className="fixed inset-0 z-[80] flex items-center justify-center overflow-y-auto bg-black/50 p-4" onClick={closeEditing}>
          <div ref={editDialogRef} role="dialog" aria-modal="true" aria-labelledby="profile-edit-heading" aria-describedby="profile-edit-description" className="relative my-auto w-full max-w-lg border border-border bg-card p-6 sm:p-8" onClick={(event) => event.stopPropagation()}>
            <p className="ef-eyebrow mb-2">// EDIT PROFILE</p>
            <h2 id="profile-edit-heading" className="text-2xl font-bold">编辑资料</h2>
            <p id="profile-edit-description" className="mt-3 text-sm leading-7 text-muted-foreground">更新你的昵称、简介和头像。</p>
            <form className="ef-form mt-6 space-y-5" onSubmit={(event) => { event.preventDefault(); void handleSaveProfile() }} aria-busy={saving}>
              <div><label htmlFor="profile-edit-nickname" className="mb-2 block text-sm font-semibold">昵称</label><Input id="profile-edit-nickname" value={editNickname} onChange={(e) => setEditNickname(e.target.value)} disabled={saving} placeholder="输入昵称" /></div>
              <div><label htmlFor="profile-edit-bio" className="mb-2 block text-sm font-semibold">简介</label><textarea id="profile-edit-bio" value={editBio} onChange={(e) => setEditBio(e.target.value)} disabled={saving} className="min-h-28 w-full resize-y border border-border bg-background p-3 text-sm leading-7 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground" placeholder="介绍一下自己..." /></div>
              <div><label htmlFor="profile-edit-avatar" className="mb-2 block text-sm font-semibold">头像链接</label><Input id="profile-edit-avatar" type="text" value={editAvatar} onChange={(e) => setEditAvatar(e.target.value)} disabled={saving} placeholder="https://..." /></div>
              {saveError && <p role="alert" className="border border-border border-l-4 border-l-foreground px-4 py-3 text-sm leading-7"><span className="mr-2 inline-block h-2 w-2 bg-accent" aria-hidden="true" />{saveError}</p>}
              <div className="flex flex-wrap justify-end gap-3 border-t border-border pt-5"><Button type="button" variant="outline" onClick={closeEditing} disabled={saving}>取消</Button><Button type="submit" disabled={saving}>{saving ? '保存中...' : '保存'}</Button></div>
            </form>
          </div>
        </div>
      )}
      {uid && <FollowDialog open={dialogOpen} onClose={() => setDialogOpen(false)} mode={dialogMode} userId={uid} />}
    </div>
  )
}
