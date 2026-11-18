import React, { useEffect, useState } from 'react'
import { Moon, Sun } from 'lucide-react'
import { Button } from './ui/button'

export default function ThemeToggle() {
  const [dark, setDark] = useState(() => {
    try { return localStorage.getItem('feedvideo-theme') === 'dark' } catch { return document.documentElement.classList.contains('dark') }
  })
  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark ? '#1b1a17' : '#f3f1ec')
  }, [dark])
  const toggle = () => {
    const next = !dark
    setDark(next)
    try { localStorage.setItem('feedvideo-theme', next ? 'dark' : 'light') } catch { /* Session-only theme when storage is unavailable. */ }
  }
  return <Button variant="ghost" size="icon" onClick={toggle} aria-label={dark ? '切换到浅色主题' : '切换到深色主题'} title={dark ? '切换到浅色主题' : '切换到深色主题'}>
    {dark ? <Sun aria-hidden="true" /> : <Moon aria-hidden="true" />}
  </Button>
}
