import React, { useRef, useEffect } from 'react'
import { Link } from 'react-router-dom'
import gsap from 'gsap'
import { Button } from '../components/ui/button'
import { useReducedMotion } from '../hooks/useReducedMotion'
import { shouldAnimate } from '../lib/animationRegistry'

export default function NotFoundPage() {
  const ref = useRef<HTMLDivElement>(null)
  const reducedMotion = useReducedMotion()

  useEffect(() => {
    if (reducedMotion) return
    if (!shouldAnimate('notFound')) return
    const ctx = gsap.context(() => {
      gsap.from(ref.current, { scale: 0.8, opacity: 0, duration: 0.5, ease: 'power2.inOut' })
    }, ref)
    return () => ctx.revert()
  }, [])

  return (
    <div ref={ref} className="text-center mt-24">
      <h1 className="text-7xl font-black text-primary/20">404</h1>
      <p className="text-muted-foreground mt-2 mb-6">页面走丢了</p>
      <Link to="/"><Button>返回首页</Button></Link>
    </div>
  )
}
