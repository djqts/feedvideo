import React from 'react'

interface GridSkeletonProps {
  rows?: number
  cols?: number
}

export function GridSkeleton({ rows = 2, cols = 3 }: GridSkeletonProps) {
  const total = rows * cols
  return (
    <div
      className="grid gap-3"
      style={{ gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))` }}
    >
      {Array.from({ length: total }).map((_, i) => (
        <div key={i} className="skeleton-grid-card animate-pulse rounded-xl border bg-card p-4">
          {/* Cover placeholder */}
          <div className="w-full aspect-[3/4] bg-muted rounded-lg" />
          {/* Title line */}
          <div className="mt-3 h-4 bg-muted rounded w-3/4" />
          {/* Subtitle line */}
          <div className="mt-2 h-3 bg-muted rounded w-1/2" />
        </div>
      ))}
    </div>
  )
}
