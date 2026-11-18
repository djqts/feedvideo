import React, { useState, useEffect, useRef, useCallback, useId } from 'react'
import { useSearchParams, Link } from 'react-router-dom'
import { ArrowRight, Hash, Play, Search, TrendingUp, UserRound, X } from 'lucide-react'
import { search, suggest, searchUsers, hotWords } from '../api/search'
import type { SearchCategories, SearchCategory, SearchResponse, SearchUser } from '../api/search'
import { Input } from '../components/ui/input'
import { Button } from '../components/ui/button'
import PageHeader from '../components/PageHeader'
import { classifyError } from '../lib/classifyError'
import type { ClassifiedError } from '../lib/classifyError'
import { ListSkeleton } from '../components/skeletons/ListSkeleton'
import ErrorState from '../components/ErrorState'

const SEARCH_TABS = ['all', 'videos', 'users', 'tags', 'hot'] as const
type SearchTab = (typeof SEARCH_TABS)[number]
const tabLabels: Record<SearchTab, string> = { all: '综合', videos: '视频', users: '用户', tags: '标签', hot: '热词' }
const categoryLabels: Record<keyof SearchCategories, string> = { video: '视频', user: '用户', tag: '标签' }

function categoryItems<T>(category: SearchCategory<T> | undefined, legacy: T[] | null | undefined): T[] {
  if (category) return category.status === 'ok' ? category.items : []
  return legacy || []
}

function SearchCover({ src }: { src?: string }) {
  const [failed, setFailed] = useState(false)
  if (!src || failed) return <span role="img" aria-label="视频封面不可用"><Play className="size-7 text-muted-foreground" strokeWidth={1.2} aria-hidden="true" /></span>
  return <img src={src} alt="" loading="lazy" className="size-full object-cover" onError={() => setFailed(true)} />
}

function UserResult({ user }: { user: SearchUser }) {
  const id = user.user_id || user.id || user.uid
  const name = user.nickname || user.username || `用户 ${id}`
  return (
    <Link to={`/profile/${id}`} className="ef-record result-row flex items-center gap-4 py-4">
      <div className="flex size-14 shrink-0 items-center justify-center overflow-hidden bg-muted text-lg font-semibold">
        {user.avatar_url ? <img src={user.avatar_url} alt="" className="size-full object-cover" loading="lazy" /> : <UserRound className="size-6 text-muted-foreground" aria-hidden="true" />}
      </div>
      <div className="min-w-0 flex-1">
        <p className="font-semibold">{name}</p>
        {user.username && user.username !== name && <p className="ef-number text-sm mt-1 text-xs text-muted-foreground">@{user.username}</p>}
        {user.bio && <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">{user.bio}</p>}
      </div>
      <ArrowRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
    </Link>
  )
}

export default function SearchPage() {
  const [sp] = useSearchParams()
  const [q, setQ] = useState(sp.get('q') || '')
  const initialTab = sp.get('tab')
  const [tab, setTab] = useState<SearchTab>(SEARCH_TABS.includes(initialTab as SearchTab) ? initialTab as SearchTab : 'all')
  const [results, setResults] = useState<SearchResponse | null>(null)
  const [userResults, setUserResults] = useState<SearchUser[]>([])
  const [hot, setHot] = useState<any[]>([])
  const [error, setError] = useState<ClassifiedError | null>(null)
  const [loading, setLoading] = useState(false)
  const [searchTrigger, setSearchTrigger] = useState(0)
  const [suggestions, setSuggestions] = useState<string[]>([])
  const [showSuggest, setShowSuggest] = useState(false)
  const [suggestLoading, setSuggestLoading] = useState(false)
  const [activeSuggestion, setActiveSuggestion] = useState(-1)
  const suggestRef = useRef<HTMLDivElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const searchPageRef = useRef<{ page: number; cursor?: string }>({ page: 1 })
  const suggestAbortRef = useRef<AbortController | null>(null)
  const inputFocusedRef = useRef(false)
  const dismissedQueryRef = useRef<string | null>(null)
  const inputId = useId()
  const suggestionsId = `${inputId}-suggestions`

  useEffect(() => {
    let active = true
    hotWords().then((r) => { if (active) setHot(r.data.data || []) }).catch(() => {})
    return () => { active = false }
  }, [])

  const doSearch = useCallback(async () => {
    abortRef.current?.abort()
    if (!q.trim() || tab === 'hot') {
      abortRef.current = null
      setResults(null)
      setUserResults([])
      setError(null)
      setLoading(false)
      return
    }
    const controller = new AbortController()
    abortRef.current = controller
    setLoading(true)
    setError(null)
    try {
      if (tab === 'users') {
        const r = await searchUsers(q.trim(), 1, 20, controller.signal)
        if (controller.signal.aborted || abortRef.current !== controller) return
        // User search returns a pagination object directly, unlike the unified search envelope.
        setUserResults(r.data?.items || [])
        setResults(null)
      } else {
        const type = tab === 'videos' ? 'video' : tab === 'tags' ? 'tag' : 'all'
        const { page, cursor } = searchPageRef.current
        const r = cursor
          ? await search(q.trim(), type, page, 20, controller.signal, cursor)
          : await search(q.trim(), type, page, 20, controller.signal)
        if (controller.signal.aborted || abortRef.current !== controller) return
        setResults(r.data.data || null)
        setUserResults([])
      }
    } catch (err: unknown) {
      if (controller.signal.aborted || abortRef.current !== controller || (err as { code?: string })?.code === 'ERR_CANCELED') return
      const classified = classifyError(err)
      if (tab !== 'users' && (err as { response?: { status?: number } })?.response?.status === 503) {
        setResults(null)
        setUserResults([])
        setError({ ...classified, message: '搜索内容暂时无法验证，请稍后重试。' })
      } else {
        setError(classified)
      }
    } finally {
      if (!controller.signal.aborted && abortRef.current === controller) setLoading(false)
    }
  }, [q, tab])

  useEffect(() => {
    abortRef.current?.abort()
    searchPageRef.current = { page: 1 }
    setResults(null)
    setUserResults([])
    setError(null)
    if (!q.trim() || tab === 'hot') {
      setLoading(false)
      return
    }
    setLoading(true)
    const timer = setTimeout(doSearch, 300)
    return () => {
      clearTimeout(timer)
      abortRef.current?.abort()
    }
  }, [q, tab, searchTrigger, doSearch])

  useEffect(() => {
    suggestAbortRef.current?.abort()
    setSuggestions([])
    setShowSuggest(false)
    setActiveSuggestion(-1)
    setSuggestLoading(false)
    if (q.trim().length < 2 || dismissedQueryRef.current === q || tab === 'hot') return
    const controller = new AbortController()
    suggestAbortRef.current = controller
    setSuggestLoading(true)
    const timer = setTimeout(async () => {
      try {
        const r = await suggest(q.trim(), 8, controller.signal)
        if (controller.signal.aborted || suggestAbortRef.current !== controller) return
        const raw = r.data?.data?.suggestions || r.data?.data || []
        const terms = Array.isArray(raw)
          ? raw.map((s: any) => typeof s === 'string' ? s : s.word || s.text || s.term || '').filter(Boolean)
          : []
        setSuggestions(terms)
        setShowSuggest(inputFocusedRef.current && dismissedQueryRef.current !== q && terms.length > 0)
      } catch {
        if (!controller.signal.aborted) setSuggestions([])
      } finally {
        if (!controller.signal.aborted) setSuggestLoading(false)
      }
    }, 200)
    return () => { clearTimeout(timer); controller.abort() }
  }, [q, tab])

  useEffect(() => {
    const handler = (event: MouseEvent) => {
      if (suggestRef.current && !suggestRef.current.contains(event.target as Node)) setShowSuggest(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  useEffect(() => () => { abortRef.current?.abort(); suggestAbortRef.current?.abort() }, [])

  const chooseTerm = (term: string) => {
    dismissedQueryRef.current = term
    suggestAbortRef.current?.abort()
    setQ(term)
    if (tab === 'hot') setTab('all')
    setShowSuggest(false)
    setActiveSuggestion(-1)
    setSearchTrigger((n) => n + 1)
  }

  const handleInputKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      dismissedQueryRef.current = q
      suggestAbortRef.current?.abort()
      setShowSuggest(false)
      setSuggestLoading(false)
      setActiveSuggestion(-1)
      return
    }
    if (!suggestions.length) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      setShowSuggest(true)
      setActiveSuggestion((current) => event.key === 'ArrowDown'
        ? (current + 1) % suggestions.length
        : (current <= 0 ? suggestions.length - 1 : current - 1))
    } else if (event.key === 'Enter' && showSuggest && activeSuggestion >= 0) {
      event.preventDefault()
      chooseTerm(suggestions[activeSuggestion])
    }
  }

  const videos = tab === 'all' || tab === 'videos' ? categoryItems(results?.categories?.video, results?.videos) : []
  const users = tab === 'all' ? categoryItems(results?.categories?.user, results?.users) : tab === 'users' ? userResults : []
  const topics = tab === 'all' || tab === 'tags'
    ? categoryItems(results?.categories?.tag, results?.tags).map((tag) => ({ name: tag.tag, video_count: tag.count }))
    : []
  const requestedCategories: (keyof SearchCategories)[] = tab === 'all' ? ['video', 'user', 'tag'] : tab === 'videos' ? ['video'] : tab === 'tags' ? ['tag'] : []
  const unavailableCategories = requestedCategories.filter((category) => results?.categories?.[category]?.status === 'unavailable')
  const partial = tab !== 'users' && (results?.partial === true || unavailableCategories.length > 0)
  const resultCount = videos.length + users.length + topics.length
  const canContinue = (tab === 'all' || tab === 'videos') && results?.has_more === true && !!results.next_cursor && !results.scan_limit_reached
  const showHot = !loading && !error && (tab === 'hot' || !q.trim())
  const showResults = !loading && !error && tab !== 'hot' && !!q.trim()

  return (
    <div>
      <PageHeader eyebrow="SEARCH" title="搜索内容" description="从一个关键词开始，找到视频、创作者和话题。" />
      <form className="ef-form mb-6 max-w-3xl" role="search" onSubmit={(event) => {
        event.preventDefault()
        dismissedQueryRef.current = q
        setShowSuggest(false)
        setSearchTrigger((n) => n + 1)
      }}>
        <label htmlFor={inputId} className="mb-2 block text-sm font-semibold">搜索视频、用户或标签</label>
        <div className="flex gap-2 sm:gap-3">
          <div className="relative min-w-0 flex-1" ref={suggestRef}>
            <Search className="pointer-events-none absolute left-3 top-4 z-10 size-5 text-muted-foreground" aria-hidden="true" />
            <Input
              id={inputId}
              role="combobox"
              aria-autocomplete="list"
              aria-expanded={showSuggest && suggestions.length > 0}
              aria-controls={suggestionsId}
              aria-activedescendant={showSuggest && activeSuggestion >= 0 ? `${suggestionsId}-${activeSuggestion}` : undefined}
              className="min-h-[52px] pl-10 pr-12 text-base"
              placeholder="搜索视频/用户/标签..."
              value={q}
              onChange={(event) => { dismissedQueryRef.current = null; setQ(event.target.value) }}
              onFocus={() => { inputFocusedRef.current = true; if (suggestions.length) setShowSuggest(true) }}
              onBlur={() => { inputFocusedRef.current = false; setShowSuggest(false) }}
              onKeyDown={handleInputKeyDown}
            />
            {q && <button type="button" aria-label="清空搜索" className="absolute right-1 top-1 flex size-11 items-center justify-center text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => { dismissedQueryRef.current = null; setQ('') }}><X className="size-4" aria-hidden="true" /></button>}
            {showSuggest && suggestions.length > 0 && (
              <div className="absolute left-0 right-0 top-full z-50 mt-1 border border-border bg-card" id={suggestionsId} role="listbox" aria-label="搜索建议">
                {suggestions.map((term, index) => (
                  <button key={`${term}-${index}`} id={`${suggestionsId}-${index}`} type="button" role="option" tabIndex={-1} aria-selected={activeSuggestion === index}
                    className={`flex min-h-12 w-full items-center gap-3 px-4 text-left text-sm ${activeSuggestion === index ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'}`}
                    onClick={() => chooseTerm(term)} onMouseDown={(event) => event.preventDefault()}>
                    <Search className="size-4 shrink-0" aria-hidden="true" /><span className="min-w-0 break-words">{term}</span>
                  </button>
                ))}
              </div>
            )}
          </div>
          <Button type="submit" className="min-h-[52px] px-4 sm:px-6">搜索</Button>
        </div>
        {suggestLoading && <p role="status" className="mt-2 text-xs text-muted-foreground">正在加载搜索建议…</p>}
      </form>

      <div className="mb-8 flex flex-wrap items-center justify-between gap-4 border-y border-border py-3">
        <div className="flex flex-wrap gap-1 sm:gap-2" role="group" aria-label="搜索分类">
          {SEARCH_TABS.map((type) => <button key={type} type="button" aria-pressed={tab === type} className="ef-tab min-h-12 px-4 sm:px-6" onClick={() => setTab(type)}>{tabLabels[type]}</button>)}
        </div>
        {showResults && resultCount > 0 && <p className="ef-number text-sm text-xs text-muted-foreground">当前显示 <span className="ef-number">{resultCount}</span> 项结果</p>}
      </div>

      <div aria-busy={loading}>
        {loading && tab !== 'hot' && <ListSkeleton n={5} />}
        {tab !== 'hot' && error && <ErrorState error={error} onRetry={doSearch} />}
        {showHot && <section className="max-w-3xl" aria-label="热门搜索">
          <div className="mb-4 flex items-center gap-2"><TrendingUp className="size-4" aria-hidden="true" /><h2 className="font-semibold">热门搜索</h2></div>
          {hot.length > 0 ? <div className="grid gap-x-8 sm:grid-cols-2">{hot.map((term: any, index: number) => (
            <button key={term.word} type="button" onClick={() => chooseTerm(term.word)} className="ef-record result-row flex min-h-16 items-center gap-4 py-3 text-left">
              <span className={`ef-number flex size-8 shrink-0 items-center justify-center text-sm ${index < 3 ? 'bg-accent text-accent-foreground' : 'bg-muted text-muted-foreground'}`}>{String(index + 1).padStart(2, '0')}</span>
              <span className="min-w-0 flex-1 break-words text-sm font-semibold">{term.word}</span>
              {tab === 'hot' && Number.isFinite(term.score) && <span className="ef-number text-sm text-xs text-muted-foreground">{Math.round(term.score)}</span>}
              <ArrowRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            </button>
          ))}</div> : <p className="ef-empty px-4 py-12 text-center text-sm text-muted-foreground">输入关键词后，相关视频、用户和话题会出现在这里。</p>}
        </section>}

        {showResults && <div className="max-w-4xl space-y-8">
          {unavailableCategories.map((category) => <p key={category} role="status" className="text-sm text-muted-foreground">{categoryLabels[category]}搜索暂不可用，请稍后重试。</p>)}
          {partial && unavailableCategories.length === 0 && <p role="status" className="text-sm text-muted-foreground">部分搜索暂不可用，请稍后重试。</p>}
          {videos.length > 0 && <section aria-label="视频结果">
            <h2 className="ef-eyebrow mb-2">// 视频 <span className="ef-number">{videos.length}</span></h2>
            {videos.map((video) => <Link key={video.id} to={`/videos/${video.id}`} className="ef-record result-row flex items-center gap-4 py-4 sm:gap-6">
              <div className="flex aspect-[4/3] w-28 shrink-0 items-center justify-center overflow-hidden bg-muted sm:w-40">
                <SearchCover key={video.cover_url || ''} src={video.cover_url} />
              </div>
              <div className="min-w-0 flex-1"><h3 className="line-clamp-2 text-sm font-semibold leading-6 sm:text-base">{video.title || '未命名视频'}</h3>
                {video.description && <p className="mt-1 hidden line-clamp-2 text-sm text-muted-foreground sm:block">{video.description}</p>}
                {(video.play_count != null || video.stats?.play_count != null) && <p className="ef-number text-sm mt-2 text-xs text-muted-foreground">{video.play_count ?? video.stats?.play_count} 次播放</p>}
              </div>
              <ArrowRight className="hidden size-4 shrink-0 text-muted-foreground sm:block" aria-hidden="true" />
            </Link>)}
          </section>}
          {users.length > 0 && <section aria-label="用户结果"><h2 className="ef-eyebrow mb-2">// 用户 <span className="ef-number">{users.length}</span></h2>{users.map((user) => <UserResult key={user.user_id || user.id || user.uid} user={user} />)}</section>}
          {topics.length > 0 && <section aria-label="话题结果"><h2 className="ef-eyebrow mb-2">// 话题 <span className="ef-number">{topics.length}</span></h2>{topics.map((topic) => <Link key={topic.name} to={`/topics/${encodeURIComponent(topic.name)}`} className="ef-record result-row flex items-center gap-4 py-5">
            <span className="flex size-10 shrink-0 items-center justify-center bg-muted"><Hash className="size-5" aria-hidden="true" /></span><span className="min-w-0 flex-1 font-semibold">{topic.name}</span>{topic.video_count != null && <span className="ef-number text-sm text-xs text-muted-foreground">{topic.video_count} 个视频</span>}<ArrowRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          </Link>)}</section>}
          {resultCount === 0 && (!partial || canContinue) && <div className="ef-empty px-4 py-16 text-center"><Search className="mx-auto mb-4 size-7 text-muted-foreground" aria-hidden="true" /><p className="font-semibold">{canContinue ? '本页暂无可展示结果' : `没有找到“${q}”相关${tab === 'users' ? '用户' : tab === 'tags' ? '话题' : '内容'}`}</p><p className="mt-2 text-sm text-muted-foreground">{canContinue ? '可以继续查找后续结果。' : '换一个关键词，或试试其他分类。'}</p></div>}
          {canContinue && <Button type="button" variant="outline" onClick={() => {
            searchPageRef.current = { page: searchPageRef.current.page + 1, cursor: results?.next_cursor }
            void doSearch()
          }}>继续查找</Button>}
        </div>}
      </div>
    </div>
  )
}
