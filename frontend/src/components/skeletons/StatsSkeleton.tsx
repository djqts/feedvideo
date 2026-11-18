import React from 'react'

export function StatsSkeleton() {
  return (
    <div className="grid grid-cols-3 gap-3">
      {Array.from({ length: 8 }).map((_, i) => (
        <div key={i} className="skeleton-stat-card animate-pulse rounded-xl border bg-card p-4">
          {/* Icon placeholder */}
          <div className="h-5 w-5 bg-muted rounded mb-2" />
          {/* Value */}
          <div className="h-6 bg-muted rounded w-16 mb-1" />
          {/* Label */}
          <div className="h-3 bg-muted rounded w-10" />
        </div>
      ))}
    </div>
  )
}
