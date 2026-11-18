import React from 'react'

interface ListSkeletonProps {
  n?: number
}

export function ListSkeleton({ n = 5 }: ListSkeletonProps) {
  return (
    <div className="space-y-3">
      {Array.from({ length: n }).map((_, i) => (
        <div key={i} className="skeleton-row animate-pulse flex items-center gap-3 p-3">
          {/* Avatar circle placeholder */}
          <div className="h-10 w-10 rounded-full bg-muted shrink-0" />
          {/* 2 text lines */}
          <div className="flex-1 space-y-2">
            <div className="h-4 bg-muted rounded w-2/3" />
            <div className="h-3 bg-muted rounded w-1/3" />
          </div>
        </div>
      ))}
    </div>
  )
}
