import React from 'react'
import { useSlowNetwork } from '../hooks/useSlowNetwork'

export default function SlowNetworkBar() {
  const { showSlow, lastSlowDuration } = useSlowNetwork()

  if (!showSlow) return null

  return (
    <div className="fixed bottom-16 md:bottom-0 left-0 right-0 z-40 bg-amber-100 text-amber-800 text-sm py-2 px-4 text-center shadow-md">
      网络较慢 (请求耗时 {lastSlowDuration}s)
    </div>
  )
}
