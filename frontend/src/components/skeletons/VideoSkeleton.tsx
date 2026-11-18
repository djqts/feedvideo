import React from 'react'

export function VideoSkeleton() {
  return (
    <div className="max-w-2xl mx-auto animate-pulse">
      {/* Player area placeholder — aspect-video */}
      <div className="w-full aspect-video bg-muted rounded-t-xl" />
      {/* Title line */}
      <div className="p-6 space-y-3">
        <div className="h-6 bg-muted rounded w-2/3" />
        {/* Description lines */}
        <div className="h-4 bg-muted rounded w-full" />
        <div className="h-4 bg-muted rounded w-5/6" />
        {/* Stats */}
        <div className="flex gap-6 mt-2">
          <div className="h-3 bg-muted rounded w-16" />
          <div className="h-3 bg-muted rounded w-16" />
          <div className="h-3 bg-muted rounded w-16" />
        </div>
      </div>
    </div>
  )
}
