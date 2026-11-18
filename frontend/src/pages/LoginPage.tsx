import React, { useState, useRef } from 'react'
import { toast } from 'sonner'
import api from '../api/client'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'

export default function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [isRegister, setIsRegister] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const pendingRef = useRef(false)

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (pendingRef.current) return
    if (!username.trim()) { setError('请填写用户名'); return }
    if (passwordValidation) { setError(passwordValidation); return }
    pendingRef.current = true
    setSubmitting(true)
    setError('')
    setNotice('')
    const url = isRegister ? '/v1/auth/register' : '/v1/auth/login'
    try {
      await api.post(url, { username, password }, { skipAuthRefresh: true })
      if (isRegister) {
        toast.success('注册成功，请登录')
        setNotice('注册成功，请使用新账号登录。')
        setPassword('')
        setIsRegister(false)
        return
      }
      window.location.href = '/'
    } catch (e: any) {
      const message = e.response?.data?.error || (isRegister ? '注册失败，请稍后重试' : '登录失败，请检查用户名和密码')
      setError(message)
      toast.error(message)
    } finally {
      pendingRef.current = false
      setSubmitting(false)
    }
  }

  const passwordBytes = new TextEncoder().encode(password).length
  const passwordValidation = !password ? '请填写密码'
    : passwordBytes > 72 ? '密码最多 72 个 UTF-8 字节'
    : isRegister && Array.from(password).length < 8 ? '密码至少 8 个字符'
    : isRegister && (!/[A-Za-z]/.test(password) || !/[0-9]/.test(password)) ? '密码须同时包含英文字母和数字'
    : ''
  const hasPasswordError = password.length > 0 && Boolean(passwordValidation)

  return (
    <div className="mx-auto grid min-h-[72vh] max-w-6xl items-center gap-6 lg:grid-cols-[1.15fr_1fr] lg:gap-16 lg:py-16">
      <section className="min-w-0" aria-label="FeedVideo 视频社区">
        <p className="ef-eyebrow mb-5 hidden lg:block">// FEEDVIDEO</p>
        <div className="hidden text-[clamp(2.5rem,6vw,5rem)] font-extrabold leading-none tracking-tight lg:block">FEED<br />VIDEO<span className="ml-2 inline-block h-3 w-3 bg-foreground" aria-hidden="true" /></div>
        <h1 className="text-2xl font-bold leading-relaxed sm:text-3xl lg:mt-8">好内容，值得被看见。</h1>
        <p className="mt-3 max-w-md text-sm leading-7 text-muted-foreground lg:mt-4 lg:text-base lg:leading-8">发现感兴趣的视频，关注喜欢的创作者，也把你的创作分享给更多人。</p>
        <div className="mt-8 hidden flex-wrap gap-x-6 gap-y-3 border-t border-border pt-5 text-sm text-muted-foreground lg:flex">
          <span>发现视频</span><span>关注创作者</span><span>分享作品</span>
        </div>
      </section>
      <section className="ef-panel w-full min-w-0 p-6 sm:p-10" aria-labelledby="login-heading">
        <p className="ef-eyebrow mb-2">// {isRegister ? 'REGISTER' : 'LOGIN'}</p>
        <h2 id="login-heading" className="text-3xl font-bold">{isRegister ? '创建账号' : '欢迎回来'}</h2>
        <p className="mt-3 text-sm leading-7 text-muted-foreground">{isRegister ? '注册后即可关注、收藏和发布视频。' : '登录 FeedVideo，继续发现与分享。'}</p>
        <form className="ef-form mt-8 space-y-5" onSubmit={submit} aria-busy={submitting} noValidate>
          <div>
            <label htmlFor="login-username" className="mb-2 block text-sm font-semibold">用户名</label>
            <Input id="login-username" name="username" autoComplete="username" placeholder="用户名" value={username} disabled={submitting} onChange={(e) => { setUsername(e.target.value); setError('') }} />
          </div>
          <div>
            <label htmlFor="login-password" className="mb-2 block text-sm font-semibold">密码</label>
            <Input id="login-password" name="password" type="password" autoComplete={isRegister ? 'new-password' : 'current-password'} placeholder={isRegister ? '密码（至少8个字符）' : '密码'} value={password} disabled={submitting} onChange={(e) => { setPassword(e.target.value); setError('') }} aria-describedby={hasPasswordError ? 'password-error' : 'password-hint'} aria-invalid={hasPasswordError} />
            {hasPasswordError ? <p id="password-error" className="mt-2 bg-accent px-3 py-2 text-sm text-accent-foreground">{passwordValidation}</p> : <p id="password-hint" className="mt-2 text-xs leading-6 text-muted-foreground">{isRegister ? '密码至少 8 个字符，须包含英文字母和数字；最多 72 个 UTF-8 字节。' : '请输入原密码，最多 72 个 UTF-8 字节。'}</p>}
          </div>
          {error && <p role="alert" className="border border-border border-l-4 border-l-foreground bg-card px-4 py-3 text-sm leading-6"><span className="mr-2 inline-block h-2 w-2 bg-accent" aria-hidden="true" />{error}</p>}
          {notice && <p role="status" className="bg-muted px-4 py-3 text-sm leading-6">{notice}</p>}
          <Button type="submit" className="w-full" disabled={submitting}>{submitting ? (isRegister ? '注册中...' : '登录中...') : (isRegister ? '注册' : '登录')}</Button>
          <div className="border-t border-border pt-3">
            <Button type="button" variant="link" className="w-full" disabled={submitting} onClick={() => { setIsRegister(!isRegister); setError(''); setNotice('') }}>{isRegister ? '已有账号？登录' : '没有账号？注册'}</Button>
          </div>
        </form>
      </section>
    </div>
  )
}
