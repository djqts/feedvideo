import React, { type ReactNode } from 'react'

interface BannerProps {
  children: ReactNode
}

export default function Banner({ children }: BannerProps) {
  return (
    <div
      role="alert"
      className="fixed top-14 left-0 right-0 z-40 bg-amber-50 border-b border-amber-200 px-4 py-2 text-center text-sm text-amber-800"
    >
      {children}
    </div>
  )
}
