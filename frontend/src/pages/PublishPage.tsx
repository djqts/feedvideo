import React, { useState, useRef, useEffect, useCallback } from 'react'
import { useNavigate, useBlocker } from 'react-router-dom'
import PageHeader from '../components/PageHeader'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { uploadFiles, getUploadReference } from '../api/upload'
import type { UploadResult } from '../api/upload'
import { savePublishDraft, loadPublishDraft, migratePublishDraft, deletePublishDraft, DraftConflictError } from '../lib/publishDraft'
import type { PublishDraft } from '../lib/publishDraft'
import { normalizeAppError } from '../lib/appError'
import api from '../api/client'
import { toast } from 'sonner'
import { useFocusTrap } from '../hooks/useFocusTrap'
import { useAuth } from '../context/AuthContext'

type Step = 'idle' | 'uploading' | 'form' | 'publishing'
type PublishField = 'title' | 'description' | 'tags' | 'music'
const publishFields: PublishField[] = ['title', 'description', 'tags', 'music']

interface UploadProgress {
  loaded: number
  total: number | null
  phase: 'compressing' | 'uploading' | 'processing'
}

interface DraftData {
  title: string
  description: string
  tags: string
  music?: string
}

interface AuthScope {
  uid: string | null
  generation: number | undefined
}

interface DraftSession {
  scope: AuthScope
  confirmed: PublishDraft | null
  revision: number
  queue: Promise<unknown>
  cleared: boolean
}

const DRAFT_CONFLICT_MESSAGE = '发布草稿已在其他页面变更。当前填写内容已保留，请恢复草稿后再继续。'

const VIDEO_TYPES = ['video/mp4', 'video/quicktime', 'video/webm']
const DRAFT_KEY_PREFIX = 'publish-draft:'
const DRAFT_VERSION = 'v1'

function draftKey(uid: string | null): string {
  // Prefer uid, but fall back to 'anon' so the key is always named safely
  return `${DRAFT_KEY_PREFIX}${uid || 'anon'}:${DRAFT_VERSION}`
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export default function PublishPage() {
  const navigate = useNavigate()
  const abortRef = useRef<AbortController | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const routeLeaveDialogRef = useRef<HTMLDivElement>(null)

  // Task 4.4: Preview URL management
  const previewUrlRef = useRef<string | null>(null)
  const mountedRef = useRef(true)

  const { uid, snapshot } = useAuth()
  const authGeneration = snapshot?.generation
  const authScopeRef = useRef({ uid, generation: authGeneration })
  authScopeRef.current = { uid, generation: authGeneration }
  const previousAuthRef = useRef(authScopeRef.current)
  const mediaGenerationRef = useRef(0)
  const uploadReferenceRef = useRef<{ id: string; key: string; generation: number; scope: AuthScope; payload?: string; metadata?: UploadResult } | null>(null)
  const recoveryAbortRef = useRef<AbortController | null>(null)
  const publishAbortRef = useRef<AbortController | null>(null)
  const draftSessionRef = useRef<DraftSession>({ scope: { uid, generation: authGeneration }, confirmed: null, revision: 0, queue: Promise.resolve(), cleared: false })
  const draftSnapshotRef = useRef<PublishDraft | null>(null)
  const draftClearedRef = useRef(false)
  const pendingRecoveryRef = useRef<PublishDraft | null>(null)
  const publishInFlightRef = useRef<object | null>(null)

  const [step, setStep] = useState<Step>('idle')
  const [error, setError] = useState('')
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<PublishField, string>>>({})
  const [errorRetryable, setErrorRetryable] = useState(true)
  const focusFieldRef = useRef<PublishField | null>(null)
  const formRef = useRef<HTMLFormElement>(null)

  useEffect(() => {
    if (step !== 'form' || !focusFieldRef.current) return
    formRef.current?.querySelector<HTMLElement>(`#publish-${focusFieldRef.current}`)?.focus()
    focusFieldRef.current = null
  }, [step, fieldErrors])

  const clearFieldError = (field: PublishField) => setFieldErrors(previous => {
    if (!previous[field]) return previous
    const next = { ...previous }; delete next[field]; return next
  })

  const [videoFile, setVideoFile] = useState<File | null>(null)
  const [coverFile, setCoverFile] = useState<File | null>(null)
  const [videoUrl, setVideoUrl] = useState('')
  const [coverUrl, setCoverUrl] = useState('')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [tags, setTags] = useState('')
  const [music, setMusic] = useState('')
  const [isDragging, setIsDragging] = useState(false)
  const [progress, setProgress] = useState<UploadProgress | null>(null)
  // After successful publish: holds target video id (or '' for home).
  // Non-null → dirty guards are released and the redirect effect navigates.
  const [publishedTarget, setPublishedTarget] = useState<string | null>(null)
  const isDirty =
    publishedTarget === null &&
    (videoFile !== null || title !== '' || description !== '' || tags !== '' || music !== '')

  // Task 4.5: Draft state
  const [draftAvailable, setDraftAvailable] = useState(false)
  const draftTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const blocker = useBlocker(isDirty)

  const dismissLeaveDialog = useCallback(() => blocker.reset?.(), [blocker])
  useFocusTrap(blocker.state === 'blocked', routeLeaveDialogRef, undefined, dismissLeaveDialog)

  // Task 4.4: Revoke preview URL helper
  const revokePreview = useCallback(() => {
    if (previewUrlRef.current) {
      URL.revokeObjectURL(previewUrlRef.current)
      previewUrlRef.current = null
    }
  }, [])

  const clearUploadReference = useCallback(() => {
    mediaGenerationRef.current++
    uploadReferenceRef.current = null
    publishInFlightRef.current = null
    abortRef.current?.abort()
    abortRef.current = null
    pendingRecoveryRef.current = null
    recoveryAbortRef.current?.abort(); recoveryAbortRef.current = null
    publishAbortRef.current?.abort(); publishAbortRef.current = null
    setVideoUrl(''); setCoverUrl(''); setProgress(null)
  }, [])

  const resetMedia = useCallback(() => {
    clearUploadReference()
    revokePreview()
    setVideoFile(null); setCoverFile(null); setStep('idle'); setError(''); setFieldErrors({}); focusFieldRef.current = null
  }, [clearUploadReference, revokePreview])

  // Scope is read from a ref so old async callbacks also see auth changes
  // before the effect below resets the displayed media.
  const isCurrentOperation = useCallback((generation: number, scope: AuthScope) => (
    mountedRef.current && mediaGenerationRef.current === generation &&
    authScopeRef.current.uid === scope.uid && authScopeRef.current.generation === scope.generation
  ), [])

  const isCurrentDraftSession = useCallback((session: DraftSession) => (
    mountedRef.current && draftSessionRef.current === session &&
    authScopeRef.current.uid === session.scope.uid && authScopeRef.current.generation === session.scope.generation
  ), [])

  const reportDraftError = useCallback((err: unknown, session: DraftSession) => {
    if (!isCurrentDraftSession(session)) return
    setError(err instanceof DraftConflictError ? DRAFT_CONFLICT_MESSAGE : '发布草稿未能保存，请稍后重试。当前填写内容已保留。')
    if (err instanceof DraftConflictError) setDraftAvailable(true)
  }, [isCurrentDraftSession])

  // Every writer uses the last snapshot this page confirmed, after earlier
  // page operations finish. Account changes keep the old queue isolated.
  const queueDraftSave = useCallback((draft: PublishDraft, session = draftSessionRef.current, releaseIntent = false) => {
    const snapshot = structuredClone(draft)
    const result = session.queue.catch(() => {}).then(async () => {
      if (session.cleared) throw new DraftConflictError()
      const current = session.confirmed
      // Queuing a stale autosave must not grant it permission to release an
      // intent confirmed by an earlier queued publish. Only explicit release
      // paths (known validation failure or replacement) can do that.
      if (!releaseIntent && current?.phase === 'publishing' &&
          (snapshot.phase !== 'publishing' || snapshot.publish_request_id !== current.publish_request_id || snapshot.publish_payload !== current.publish_payload)) throw new DraftConflictError()
      const next = { ...snapshot, updated_at: new Date().toISOString(), local_revision: ++session.revision }
      await savePublishDraft(next, session.confirmed)
      session.confirmed = next
      return next
    })
    session.queue = result
    return result
  }, [])

  const queueDraftDelete = useCallback((session: DraftSession) => {
    const result = session.queue.catch(() => {}).then(async () => {
      if (session.scope.uid) await deletePublishDraft(session.scope.uid, session.confirmed)
      session.confirmed = null
      session.cleared = true
    })
    session.queue = result
    return result
  }, [])

  useEffect(() => {
    const previous = previousAuthRef.current
    if (previous.uid !== uid || previous.generation !== authGeneration) {
      const oldDraft = draftSnapshotRef.current
      if (oldDraft?.user_id === previous.uid && !draftClearedRef.current && (oldDraft.title || oldDraft.description || oldDraft.tags || oldDraft.music || oldDraft.upload_ref)) void queueDraftSave(oldDraft).catch(() => {})
      resetMedia()
      draftSessionRef.current = { scope: { uid, generation: authGeneration }, confirmed: null, revision: 0, queue: Promise.resolve(), cleared: false }
      draftSnapshotRef.current = null; draftClearedRef.current = false; setDraftAvailable(false)
      if (previous.uid !== uid) { setTitle(''); setDescription(''); setTags(''); setMusic('') }
    }
    previousAuthRef.current = { uid, generation: authGeneration }
  }, [uid, authGeneration, resetMedia, queueDraftSave])

  // Task 4.4: Create preview URL for a new video file
  const setVideoFileWithPreview = useCallback((file: File | null) => {
    clearUploadReference()
    // Revoke old preview URL before replacing
    revokePreview()
    if (file) {
      previewUrlRef.current = URL.createObjectURL(file)
    }
    setVideoFile(file)
    setStep('idle')
    setError('')
    setFieldErrors({}); focusFieldRef.current = null
  }, [revokePreview, clearUploadReference])

  // Task 4.4: Cleanup on unmount
  // Note: React 18 StrictMode double-mount in dev may cause the preview to be
  // revoked and re-created. In production this is not an issue. The re-creation
  // happens because setVideoFileWithPreview calls URL.createObjectURL again if
  // the file state is preserved through the double-mount cycle.
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      mediaGenerationRef.current++
      uploadReferenceRef.current = null
      publishInFlightRef.current = null
      recoveryAbortRef.current?.abort(); publishAbortRef.current?.abort()
      if (previewUrlRef.current) {
        URL.revokeObjectURL(previewUrlRef.current)
        previewUrlRef.current = null
      }
    }
  }, [])

  // Navigate after successful publish (released dirty guards first via publishedTarget)
  useEffect(() => {
    if (publishedTarget === null) return
    navigate(publishedTarget ? `/videos/${publishedTarget}` : '/', { replace: true })
  }, [publishedTarget, navigate])

  useEffect(() => {
    if (!isDirty) return
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [isDirty])

  useEffect(() => {
    return () => { abortRef.current?.abort() }
  }, [])

  // Keep the latest immutable account snapshot for pagehide/unmount saves.
  const reference = uploadReferenceRef.current
  const pendingRecovery = pendingRecoveryRef.current
  if (uid && previousAuthRef.current.uid === uid && previousAuthRef.current.generation === authGeneration && !draftClearedRef.current) draftSnapshotRef.current = { version: 2, user_id: uid, draft_id: 'active', title, description, tags, music, updated_at: new Date().toISOString(), local_revision: draftSessionRef.current.revision, phase: step === 'publishing' || reference?.payload || pendingRecovery?.publish_payload ? 'publishing' : reference?.id || pendingRecovery?.upload_ref ? 'uploaded' : 'editing', upload_ref: reference?.metadata ?? pendingRecovery?.upload_ref, publish_request_id: reference?.key || pendingRecovery?.publish_request_id || undefined, publish_payload: reference?.payload ?? pendingRecovery?.publish_payload }

  const persistDraft = useCallback(async (snapshot = draftSnapshotRef.current) => {
    if (draftClearedRef.current || !snapshot || !(snapshot.title || snapshot.description || snapshot.tags || snapshot.music || snapshot.upload_ref)) return
    const session = draftSessionRef.current
    try { await queueDraftSave(snapshot, session) }
    catch (err) { reportDraftError(err, session) }
  }, [queueDraftSave, reportDraftError])

  // Task 4.5: Check for existing draft on mount
  useEffect(() => {
    const key = draftKey(uid)
    let raw: string | null = null
    try { raw = sessionStorage.getItem(key) } catch { /* Browser storage unavailable. */ }
    if (raw) {
      try {
        const draft: DraftData = JSON.parse(raw)
        if (draft.title || draft.description || draft.tags) {
          setDraftAvailable(true)
        }
      } catch { /* corrupted draft — ignore */ }
    }
    const session = draftSessionRef.current
    if (uid) {
      const result = session.queue.catch(() => {}).then(() => loadPublishDraft(uid)).then(draft => {
        session.confirmed = draft
        session.revision = Math.max(session.revision, draft?.local_revision ?? 0)
        if (draft && isCurrentDraftSession(session)) setDraftAvailable(true)
      })
      session.queue = result
      void result.catch(err => reportDraftError(err, session))
    }
  }, [uid, authGeneration, isCurrentDraftSession, reportDraftError])

  // Task 4.5: Debounced auto-save draft
  const saveDraft = useCallback(() => {
    if (draftTimerRef.current) clearTimeout(draftTimerRef.current)
    draftTimerRef.current = setTimeout(() => {
      const data: DraftData = { title, description, tags, music: music || undefined }
      // Only save if there's meaningful content
      if (data.title || data.description || data.tags) {
        try { sessionStorage.setItem(draftKey(uid), JSON.stringify(data)) } catch { /* v2 still attempted. */ }
      }
      void persistDraft()
    }, 1000)
  }, [title, description, tags, music, uid, persistDraft])

  // Save draft when form fields change (debounced)
  useEffect(() => {
    if (step === 'form') {
      saveDraft()
    }
    return () => {
      if (draftTimerRef.current) clearTimeout(draftTimerRef.current)
    }
  }, [title, description, tags, music, step, saveDraft])

  // Cleanup draft timer on unmount
  useEffect(() => {
    const save = () => { void persistDraft() }
    window.addEventListener('pagehide', save)
    return () => {
      if (draftTimerRef.current) clearTimeout(draftTimerRef.current)
      window.removeEventListener('pagehide', save)
      void persistDraft()
    }
  }, [persistDraft])

  // Task 4.5: Clear draft
  const clearDraft = useCallback(async (published = false) => {
    const session = draftSessionRef.current
    if (draftTimerRef.current) clearTimeout(draftTimerRef.current)
    try {
      await queueDraftDelete(session)
      try { sessionStorage.removeItem(draftKey(session.scope.uid)) } catch { /* The durable record is already cleared. */ }
    } catch (err) {
      if (!published) { reportDraftError(err, session); return }
      if (isCurrentDraftSession(session)) toast.success('发布已完成，另有变更或未能清理的草稿已保留。')
    }
    if (!isCurrentDraftSession(session)) return
    // A confirmed POST stays successful even when a newer draft blocks cleanup.
    session.cleared = true
    draftClearedRef.current = true; draftSnapshotRef.current = null; setDraftAvailable(false)
  }, [queueDraftDelete, reportDraftError, isCurrentDraftSession])

  const releaseUploadIntent = useCallback(async () => {
    const session = draftSessionRef.current
    const generation = mediaGenerationRef.current
    const scope = authScopeRef.current
    const snapshot = draftSnapshotRef.current
    if (draftTimerRef.current) clearTimeout(draftTimerRef.current)
    if (snapshot && (snapshot.upload_ref || snapshot.publish_request_id || snapshot.publish_payload || session.confirmed?.upload_ref || session.confirmed?.publish_request_id || session.confirmed?.publish_payload)) {
      try { await queueDraftSave({ ...snapshot, phase: 'editing', upload_ref: undefined, publish_request_id: undefined, publish_payload: undefined }, session, true) }
      catch (err) { reportDraftError(err, session); return false }
    }
    return isCurrentOperation(generation, scope)
  }, [queueDraftSave, reportDraftError, isCurrentOperation])

  const handleReplaceVideo = useCallback(async () => {
    if (await releaseUploadIntent()) resetMedia()
  }, [releaseUploadIntent, resetMedia])

  // Task 4.5: Restore draft — fills form and enters form step
  const restoreDraft = useCallback(async () => {
    const key = draftKey(uid)
    const scope = authScopeRef.current
    const generation = mediaGenerationRef.current
    const session = draftSessionRef.current
    if (recoveryAbortRef.current) return
    const recovery = new AbortController(); recoveryAbortRef.current = recovery
    try {
      const result = session.queue.catch(() => {}).then(() => uid ? migratePublishDraft(uid) : null)
      session.queue = result
      let draft: PublishDraft | DraftData | null = await result
      if (!draft) { const raw = sessionStorage.getItem(key); if (raw) draft = JSON.parse(raw) }
      if (!draft || !isCurrentOperation(generation, scope)) return
      session.confirmed = (draft as PublishDraft).version === 2 ? structuredClone(draft as PublishDraft) : null
      session.revision = Math.max(session.revision, session.confirmed?.local_revision ?? 0)
      session.cleared = false
      draftClearedRef.current = false
      setError('')
      setTitle(draft.title || '')
      setDescription(draft.description || '')
      setTags(draft.tags || '')
      setMusic(draft.music || '')
      setDraftAvailable(false)
      // Enter form step — the draft only has text fields, not file URLs.
      // The user will see the form to continue editing; a real re-upload
      // scenario would require selecting files again.
      setStep('form')
      const stored = draft as PublishDraft
      if (!stored.upload_ref?.upload_id) return
      pendingRecoveryRef.current = stored
      const abort = recovery
      try {
        const verified = await getUploadReference(stored.upload_ref.upload_id, abort.signal)
        if (!isCurrentOperation(generation, scope)) return
        if (verified?.upload_id !== stored.upload_ref.upload_id || verified.status !== 'ready' || !verified.video_url || !stored.publish_request_id) {
          setError('文件暂不可恢复，请重新选择视频。文本草稿已保留。'); return
        }
        if (String(verified.content_version) !== String(stored.upload_ref.content_version)) { setError('文件版本已变化，请重新选择视频。'); return }
        uploadReferenceRef.current = { id: verified.upload_id, key: stored.publish_request_id, generation, scope, payload: stored.publish_payload, metadata: verified }
        pendingRecoveryRef.current = null
        setVideoUrl(verified.video_url); setCoverUrl(verified.cover_url || '')
        if (stored.publish_payload) {
          const original = JSON.parse(stored.publish_payload)
          setTitle(original.title || ''); setDescription(original.description || ''); setTags(Array.isArray(original.tags) ? original.tags.join(',') : ''); setMusic(original.music || '')
        }
      } catch (err: any) {
        if (!isCurrentOperation(generation, scope)) return
        setError(err?.response?.status === 410 ? '文件已过期，请重新选择。文本草稿已保留。' : '文件暂不可恢复，请稍后重试或重新选择。文本草稿已保留。')
      } finally { if (recoveryAbortRef.current === abort) recoveryAbortRef.current = null }
    } catch (err) { reportDraftError(err, session) }
    finally { if (recoveryAbortRef.current === recovery) recoveryAbortRef.current = null }
  }, [uid, isCurrentOperation, reportDraftError])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault(); e.stopPropagation(); setIsDragging(true)
  }, [])
  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault(); e.stopPropagation(); setIsDragging(false)
  }, [])
  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault(); e.stopPropagation(); setIsDragging(false)
    const file = e.dataTransfer.files?.[0]
    if (file && VIDEO_TYPES.includes(file.type)) { setVideoFileWithPreview(file) }
  }, [setVideoFileWithPreview])
  const handleFileInputChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (file) { setVideoFileWithPreview(file) }
  }, [setVideoFileWithPreview])

  const progressPercent = progress && progress.total ? Math.min(100, Math.round((progress.loaded / progress.total) * 100)) : 0
  const phaseLabel: Record<UploadProgress['phase'], string> = {
    compressing: '正在准备...', uploading: `正在上传 ${progressPercent}%`, processing: '正在处理...',
  }

  const handleUpload = useCallback(async () => {
    if (abortRef.current) return
    if (!videoFile) { setError('请选择视频文件'); return }
    let generation = mediaGenerationRef.current
    const scope = authScopeRef.current
    const abortController = new AbortController()
    abortRef.current = abortController
    try {
      if (!(await releaseUploadIntent()) || abortController.signal.aborted || abortRef.current !== abortController) return
      abortRef.current = null
      clearUploadReference()
      generation = mediaGenerationRef.current
      abortRef.current = abortController
      draftSessionRef.current.cleared = false; draftClearedRef.current = false
      setError(''); setStep('uploading')
      setProgress({ loaded: 0, total: videoFile.size, phase: 'compressing' })
      const result = await uploadFiles(videoFile, coverFile || undefined, {
        onProgress: (e) => {
          if (isCurrentOperation(generation, scope)) setProgress({ loaded: e.loaded, total: e.total ?? null, phase: 'uploading' })
        },
        signal: abortController.signal,
      })
      if (!isCurrentOperation(generation, scope)) return
      // A new registered upload owns one fixed publish key for this page session.
      uploadReferenceRef.current = {
        id: result.upload_id || '',
        key: result.upload_id ? `publish:${Array.from(crypto.getRandomValues(new Uint8Array(16)), (value) => value.toString(16).padStart(2, '0')).join('')}` : '',
        generation, scope,
        metadata: result,
      }
      setProgress({ loaded: result.video_url ? 1 : 0, total: 1, phase: 'processing' })
      setVideoUrl(result.video_url); setCoverUrl(result.cover_url)
      // Task 4.4: Revoke preview URL after successful upload
      revokePreview()
      setStep('form'); setProgress(null)
    } catch (err: any) {
      if (!isCurrentOperation(generation, scope)) return
      const issue = normalizeAppError(err)
      setError(issue.message); setErrorRetryable(issue.retryable); setStep('idle'); setProgress(null)
    } finally {
      if (abortRef.current === abortController) abortRef.current = null
    }
  }, [videoFile, coverFile, revokePreview, clearUploadReference, isCurrentOperation, releaseUploadIntent])

  const handleCancel = useCallback(() => {
    resetMedia()
    toast.error('上传已取消')
  }, [resetMedia])

  const handlePublish = async () => {
    // State does not redraw until the event batch ends; lock synchronously.
    if (publishInFlightRef.current) return
    const media = uploadReferenceRef.current
    if (!videoUrl || !media || !isCurrentOperation(media.generation, media.scope)) { setError('请先选择并上传视频'); return }
    if (!title.trim()) { setError(''); focusFieldRef.current = 'title'; setFieldErrors({ title: '请填写标题' }); return }
    const reference = media.id ? media : null
    const payload = {
      title: title.trim(), description, tags: tags.split(',').map((t) => t.trim()).filter(Boolean), music, video_url: videoUrl, cover_url: coverUrl,
      ...(reference ? { upload_id: reference.id } : {}),
    }
    const serializedPayload = JSON.stringify(payload)
    if (reference?.payload && reference.payload !== serializedPayload) {
      setError('上次发布结果尚未确认，请恢复原填写内容后重试，或重新选择并上传视频。')
      return
    }
    const priorPayload = reference?.payload
    if (reference) reference.payload = serializedPayload
    const operation = {}
    publishInFlightRef.current = operation
    const generation = mediaGenerationRef.current
    const scope = authScopeRef.current
    const session = draftSessionRef.current
    let persistedPublish = false
    setError(''); setFieldErrors({}); setStep('publishing')
    try {
      const abort = new AbortController(); publishAbortRef.current = abort
      if (uid && reference) {
        await queueDraftSave({ version: 2, user_id: uid, draft_id: 'active', title, description, tags, music, updated_at: new Date().toISOString(), local_revision: session.revision, phase: 'publishing', upload_ref: reference.metadata, publish_request_id: reference.key, publish_payload: reference.payload }, session)
        persistedPublish = true
      }
      if (!isCurrentOperation(generation, scope)) return
      const { data } = reference
        ? await api.post('/v1/videos', payload, { headers: { 'Idempotency-Key': reference.key }, signal: abort.signal, skipAuthRefresh: true })
        : await api.post('/v1/videos', payload)
      if (!isCurrentOperation(generation, scope)) return
      // Task 4.5: Clear draft on successful publish
      await clearDraft(true)
      if (!isCurrentOperation(generation, scope)) return
      // Clear media references before releasing the dirty guards and redirecting.
      resetMedia()
      const videoId = data?.data?.video_id
      // Release dirty guards first (blocker + beforeunload), then navigate in
      // an effect AFTER re-render — otherwise our own route blocker or the
      // browser's beforeunload prompt intercepts the redirect.
      setPublishedTarget(videoId ? String(videoId) : '')
    } catch (err: any) {
      if (!isCurrentOperation(generation, scope)) return
      if (!persistedPublish && reference) reference.payload = priorPayload
      if (err instanceof DraftConflictError) {
        reportDraftError(err, session); setStep('form'); return
      }
      // Validation 400 is known to precede reservation; corrections may keep
      // this upload/key. Unknown outcomes and reservation conflicts stay bound.
      if (err?.response?.status === 400 && reference?.payload === serializedPayload) {
        try {
          if (uid) await queueDraftSave({ version: 2, user_id: uid, draft_id: 'active', title, description, tags, music, updated_at: new Date().toISOString(), local_revision: session.revision, phase: 'uploaded', upload_ref: reference.metadata, publish_request_id: reference.key }, session, true)
          if (!isCurrentOperation(generation, scope)) return
          delete reference.payload
        } catch (saveError) {
          if (!isCurrentOperation(generation, scope)) return
          reportDraftError(saveError, session); setStep('form'); return
        }
      }
      const issue = normalizeAppError(err)
      const fields: Partial<Record<PublishField, string>> = {}
      if (issue.kind === 'validation') for (const field of publishFields) {
        if (issue.fieldErrors[field]) fields[field] = issue.fieldErrors[field]
      }
      focusFieldRef.current = publishFields.find(field => fields[field]) ?? null
      setFieldErrors(fields)
      setError(issue.message); setErrorRetryable(issue.retryable)
      setStep('form')
    } finally {
      if (publishInFlightRef.current === operation) publishInFlightRef.current = null
    }
  }

  const currentStep = step === 'publishing' ? 2 : videoUrl ? 1 : 0
  const dropZoneClasses = [
    'ef-cut min-h-[220px] sm:min-h-[300px] border border-dashed p-5 text-center cursor-pointer flex flex-col items-center justify-center focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-foreground',
    'hover:border-foreground hover:bg-muted',
    isDragging ? 'border-primary bg-accent text-accent-foreground' : 'border-border bg-card',
  ].join(' ')

  return (
    <div className="mx-auto max-w-5xl">
      {blocker.state === 'blocked' && (
        <div className="fixed inset-0 z-[80] flex items-center justify-center bg-black/50 p-4">
          <div ref={routeLeaveDialogRef} role="dialog" aria-modal="true" aria-labelledby="publish-leave-heading" aria-describedby="publish-leave-description" className="w-full max-w-sm border border-border bg-card p-6">
            <p className="ef-eyebrow mb-2">// LEAVE EDITOR</p>
            <h2 id="publish-leave-heading" className="mb-3 text-xl font-bold">放弃编辑？</h2>
            <p id="publish-leave-description" className="mb-6 text-sm leading-7 text-muted-foreground">你有未保存的内容，离开后数据将丢失。</p>
            <div className="flex flex-wrap justify-end gap-3">
              <Button variant="outline" onClick={dismissLeaveDialog}>继续编辑</Button>
              <Button onClick={() => blocker.proceed?.()}>离开</Button>
            </div>
          </div>
        </div>
      )}
      <PageHeader eyebrow="PUBLISH" title="发布视频" description="选择素材，写下介绍，让你的作品被更多人看见。" />
      <ol aria-label="发布步骤" className="mb-8 grid grid-cols-3 border-y border-border">
        {['选择素材', '填写信息', '发布'].map((label, index) => (
          <li key={label} aria-current={currentStep === index ? 'step' : undefined} className={'flex min-w-0 items-center gap-2 px-2 py-4 text-sm sm:gap-3 sm:px-5 ' + (currentStep === index ? 'bg-accent font-semibold text-accent-foreground' : 'text-muted-foreground')}>
            <span className="ef-number text-base sm:text-xl">{String(index + 1).padStart(2, '0')}</span><span>{label}</span>
          </li>
        ))}
      </ol>
      {draftAvailable && (
        <div className="mb-6 flex flex-wrap items-center justify-between gap-3 border border-border bg-card p-4">
          <span className="text-sm leading-6"><span className="mr-2 inline-block h-2 w-2 bg-accent" aria-hidden="true" />你有一条未完成的发布草稿</span>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={restoreDraft}>继续编辑</Button>
            <Button variant="ghost" size="sm" onClick={() => { void clearDraft() }}>丢弃</Button>
          </div>
        </div>
      )}
      {error && (
        <div id="publish-error" role="alert" className="mb-6 border border-border border-l-4 border-l-foreground bg-card p-4 text-sm leading-7">
          <span className="mr-2 inline-block h-2 w-2 bg-accent" aria-hidden="true" /><span>{error}</span>
          {pendingRecoveryRef.current?.upload_ref && <Button type="button" variant="outline" size="sm" className="mt-3" onClick={restoreDraft}>重试恢复文件</Button>}
          {step === 'idle' && videoFile && errorRetryable && <Button variant="link" onClick={handleUpload} className="ml-2">重试</Button>}
        </div>
      )}
      <div className="grid items-start gap-6 lg:grid-cols-[1fr_1.1fr] lg:gap-10">
        <section className="min-w-0" aria-labelledby="publish-material-heading">
          <p className="ef-eyebrow mb-2">// MEDIA</p>
          <h2 id="publish-material-heading" className="mb-4 text-lg font-bold">视频素材</h2>
          {step === 'idle' ? (
            <div data-testid="drop-zone" className={dropZoneClasses} onDragOver={handleDragOver} onDragLeave={handleDragLeave} onDrop={handleDrop} onClick={() => fileInputRef.current?.click()} role="button" aria-label="选择视频文件" tabIndex={0} onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); fileInputRef.current?.click() } }}>
              {videoFile ? (
                <div className="w-full min-w-0 space-y-3">
                  {previewUrlRef.current && <video src={previewUrlRef.current} className="max-h-64 w-full bg-muted object-contain" muted preload="metadata" />}
                  <p className="break-all text-sm font-semibold">{videoFile.name}</p>
                  <p className="ef-number text-sm text-muted-foreground">{formatSize(videoFile.size)} / 点击更换视频</p>
                </div>
              ) : (
                <div className="space-y-4">
                  <svg aria-hidden="true" className="mx-auto h-10 w-10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><rect x="3" y="5" width="18" height="14" /><path d="m10 9 5 3-5 3V9Z" /></svg>
                  <p className="text-sm leading-7">拖拽视频文件到此处，或点击选择</p>
                  <p className="ef-number text-sm text-muted-foreground">MP4 / MOV / WEBM</p>
                </div>
              )}
              <input ref={fileInputRef} type="file" accept="video/mp4,video/quicktime,video/webm" aria-label="选择视频文件，支持拖拽" className="hidden" onChange={handleFileInputChange} />
            </div>
          ) : (videoUrl || previewUrlRef.current) ? (
            <div className="border border-border bg-card">
              <video src={videoUrl || previewUrlRef.current || undefined} poster={coverUrl || undefined} controls={!!videoUrl} muted preload="metadata" className="max-h-[420px] w-full bg-muted object-contain" aria-label="视频预览" />
              {videoFile && <div className="space-y-2 border-t border-border p-4"><p className="break-all text-sm font-semibold">{videoFile.name}</p><p className="ef-number text-sm text-muted-foreground">{formatSize(videoFile.size)} / {videoUrl ? '已上传' : '上传中'}</p></div>}
              {step === 'form' && videoUrl && <Button variant="outline" className="m-4" onClick={handleReplaceVideo}>重新选择视频</Button>}
            </div>
          ) : (
            <div className="ef-empty px-6 py-10">
              <p className="text-sm leading-7">草稿已恢复。重新选择并上传视频后，就可以继续发布。</p>
              <Button variant="outline" className="mt-5" onClick={() => setStep('idle')}>选择视频</Button>
            </div>
          )}
        </section>
        <section className="ef-panel min-w-0 p-6 sm:p-8" aria-label="发布设置">
          {step === 'idle' && (
            <div className="space-y-6">
              <div><p className="ef-eyebrow mb-2">// UPLOAD</p><h2 className="text-xl font-bold">准备上传</h2><p className="mt-3 text-sm leading-7 text-muted-foreground">上传完成后，添加标题、描述和标签。</p></div>
              <div><label htmlFor="publish-cover" className="mb-2 block text-sm font-semibold">选择封面（可选）</label><Input id="publish-cover" type="file" accept="image/jpeg,image/png,image/webp" onChange={(e) => { const f = (e.target as HTMLInputElement).files?.[0]; if (f) { clearUploadReference(); setCoverFile(f) } }} /><p className="mt-2 text-xs leading-6 text-muted-foreground">支持 JPG、PNG 和 WEBP 图片。</p></div>
              <Button onClick={handleUpload} className="w-full">上传</Button>
            </div>
          )}
          {step === 'uploading' && (
            <div className="space-y-6">
              <div><p className="ef-eyebrow mb-2">// UPLOAD</p><h2 className="text-xl font-bold">上传素材</h2><p className="mt-3 text-sm leading-7 text-muted-foreground">请稍候，上传完成后可以填写视频信息。</p></div>
              {progress && (
                <div role="progressbar" aria-valuenow={progress.phase === 'compressing' ? 0 : progressPercent} aria-valuemin={0} aria-valuemax={100} aria-label="上传进度" className="space-y-3">
                  <span aria-live="polite" className="block text-sm">{phaseLabel[progress.phase]}</span>
                  <div className="h-2 w-full overflow-hidden bg-muted"><div className="h-2 bg-primary" style={{ width: (progress.phase === 'compressing' ? 10 : progressPercent) + '%' }} /></div>
                </div>
              )}
              <Button variant="outline" onClick={handleCancel} className="w-full">取消</Button>
            </div>
          )}
          {(step === 'form' || step === 'publishing') && (
            <form ref={formRef} className="ef-form space-y-5" onSubmit={(event) => { event.preventDefault(); if (step !== 'publishing') void handlePublish() }} aria-busy={step === 'publishing'} noValidate>
              <div><p className="ef-eyebrow mb-2">// VIDEO DETAILS</p><h2 className="text-xl font-bold">视频信息</h2></div>
              <div><label htmlFor="publish-title" className="mb-2 block text-sm font-semibold">标题 *</label><Input id="publish-title" value={title} disabled={step === 'publishing'} onChange={(e) => { setTitle(e.target.value); clearFieldError('title') }} placeholder="视频标题" aria-describedby={fieldErrors.title ? 'title-error' : undefined} aria-invalid={!!fieldErrors.title} />{fieldErrors.title && <div id="title-error" role="alert" className="mt-2 text-sm">{fieldErrors.title}</div>}</div>
              <div><label htmlFor="publish-description" className="mb-2 block text-sm font-semibold">描述</label><textarea id="publish-description" className="min-h-32 w-full resize-y border border-border bg-background p-3 text-base leading-7 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground" value={description} disabled={step === 'publishing'} onChange={(e) => { setDescription(e.target.value); clearFieldError('description') }} aria-invalid={!!fieldErrors.description} aria-describedby={fieldErrors.description ? 'description-error' : undefined} placeholder="视频描述" />{fieldErrors.description && <div id="description-error" role="alert" className="mt-2 text-sm">{fieldErrors.description}</div>}</div>
              <div><label htmlFor="publish-tags" className="mb-2 block text-sm font-semibold">标签（逗号分隔）</label><Input id="publish-tags" value={tags} disabled={step === 'publishing'} onChange={(e) => { setTags(e.target.value); clearFieldError('tags') }} aria-invalid={!!fieldErrors.tags} aria-describedby={fieldErrors.tags ? 'tags-error' : undefined} placeholder="标签" />{fieldErrors.tags && <div id="tags-error" role="alert" className="mt-2 text-sm">{fieldErrors.tags}</div>}</div>
              <div><label htmlFor="publish-music" className="mb-2 block text-sm font-semibold">音乐</label><Input id="publish-music" value={music} disabled={step === 'publishing'} onChange={(e) => { setMusic(e.target.value); clearFieldError('music') }} aria-invalid={!!fieldErrors.music} aria-describedby={fieldErrors.music ? 'music-error' : undefined} placeholder="音乐名称" />{fieldErrors.music && <div id="music-error" role="alert" className="mt-2 text-sm">{fieldErrors.music}</div>}</div>
              <p className="border-t border-border pt-4 text-xs leading-6 text-muted-foreground">文字内容会自动保存为当前账号的临时草稿。</p>
              <Button type="submit" disabled={step === 'publishing' || !videoUrl} className="w-full">{step === 'publishing' ? '发布中...' : '发布'}</Button>
            </form>
          )}
        </section>
      </div>
    </div>
  )
}

