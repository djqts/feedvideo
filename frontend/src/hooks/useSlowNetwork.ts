import { useState, useEffect, useRef } from 'react'

interface SlowNetworkDetail {
  duration: number
  url: string
}

export function useSlowNetwork() {
  const [showSlow, setShowSlow] = useState(false)
  const [lastSlowUrl, setLastSlowUrl] = useState('')
  const [lastSlowDuration, setLastSlowDuration] = useState(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    const handler = (e: Event) => {
      const { duration, url } = (e as CustomEvent<SlowNetworkDetail>).detail
      setLastSlowUrl(url)
      setLastSlowDuration(Math.round(duration / 1000))
      setShowSlow(true)

      if (timerRef.current) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => {
        setShowSlow(false)
      }, 5000)
    }

    window.addEventListener('slow-network', handler)
    return () => {
      window.removeEventListener('slow-network', handler)
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [])

  return { showSlow, lastSlowUrl, lastSlowDuration }
}
