import React from 'react'

interface FeedSkeletonProps {
  count?: number
}

export function FeedSkeleton({ count = 3 }: FeedSkeletonProps) {
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-x-4 gap-y-8 sm:gap-x-6">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="skeleton-card animate-pulse bg-card border-b pb-4">
          {/* Cover placeholder — aspect-[3/4] */}
          <div className="w-full aspect-[3/4] bg-muted rounded-lg" />
          {/* 2 lines of text placeholder */}
          <div className="mt-3 space-y-2">
            <div className="h-4 bg-muted rounded w-3/4" />
            <div className="h-3 bg-muted rounded w-1/2" />
          </div>
          {/* Stats line */}
          <div className="mt-3 flex gap-4">
            <div className="h-3 bg-muted rounded w-12" />
            <div className="h-3 bg-muted rounded w-12" />
            <div className="h-3 bg-muted rounded w-12" />
          </div>
        </div>
      ))}
    </div>
  )
}
