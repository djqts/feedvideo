import React, { useState, useCallback } from 'react'
import type { ClassifiedError } from '../lib/classifyError'
import { Button } from './ui/button'
import { cn } from '../lib/utils'
import { Clock3, FileQuestion, LockKeyhole, TriangleAlert, WifiOff } from 'lucide-react'

interface ErrorStateProps {
  error: ClassifiedError
  onRetry?: () => Promise<void> | void
  className?: string
}

const errorIcons = {
  network: WifiOff,
  timeout: Clock3,
  forbidden: LockKeyhole,
  notfound: FileQuestion,
  server: TriangleAlert,
  validation: TriangleAlert,
  auth: LockKeyhole,
  auth_recovered: LockKeyhole,
  conflict: TriangleAlert,
  rate_limit: Clock3,
  canceled: TriangleAlert,
}

export default function ErrorState({ error, onRetry, className }: ErrorStateProps) {
  const [retrying, setRetrying] = useState(false)

  const handleRetry = useCallback(async () => {
    if (!onRetry || retrying) return
    setRetrying(true)
    try {
      await onRetry()
    } catch {
      // onRetry failure — just reset the button state
    } finally {
      setRetrying(false)
    }
  }, [onRetry, retrying])

  const showRetry = error.retriable && onRetry
  const Icon = errorIcons[error.type]
  if (error.type === 'canceled') return null

  return (
    <div
      role="alert"
      className={cn(
        'flex flex-col items-center justify-center border border-dashed bg-card text-center py-14 px-5',
        className,
      )}
    >
      <span className="flex h-12 w-12 items-center justify-center bg-accent text-accent-foreground mb-5" aria-hidden="true">
        <Icon size={24} strokeWidth={1.5} />
      </span>
      <p className="text-muted-foreground text-sm mb-4 max-w-sm">{error.message}</p>
      {error.requestId && (
        <details className="mb-4 max-w-full break-all text-sm text-muted-foreground">
          <summary className="cursor-pointer">诊断详情</summary>
          <p className="mt-2">请求编号：{error.requestId}</p>
        </details>
      )}
      {showRetry && (
        <Button
          variant="outline"
          size="sm"
          onClick={handleRetry}
          disabled={retrying}
        >
          {retrying ? '重试中...' : '重试'}
        </Button>
      )}
    </div>
  )
}
