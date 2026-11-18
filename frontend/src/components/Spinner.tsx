import React from 'react'

export default function Spinner() {
  return (
    <div className="flex items-center justify-center gap-3 p-12" role="status" aria-label="加载中">
      <span className="h-3 w-3 animate-pulse bg-foreground" aria-hidden="true" />
      <span className="text-sm text-muted-foreground">加载中...</span>
    </div>
  )
}
