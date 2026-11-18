import React, { useState, useEffect, useCallback, useRef } from 'react'
import { Button } from './ui/button'
import { useFocusTrap } from '../hooks/useFocusTrap'

interface DangerConfirmProps {
  title: string
  description: string
  confirmText: string
  /** 操作后果说明文本，用于 requireCountdown=false 时确认 */
  consequenceText?: string
  /** 是否需要倒计时确认（默认 false，立即可用） */
  requireCountdown?: boolean
  onConfirm: () => void
  children: React.ReactNode
}

export default function DangerConfirm({
  title,
  description,
  confirmText,
  consequenceText,
  requireCountdown = false,
  onConfirm,
  children,
}: DangerConfirmProps) {
  const [open, setOpen] = useState(false)
  const [countdown, setCountdown] = useState(3)
  const [confirmed, setConfirmed] = useState(false)
  const dialogRef = useRef<HTMLDivElement>(null)

  // Task 3.4: Focus trap for dialog
  useFocusTrap(open, dialogRef)

  useEffect(() => {
    if (!open) {
      setCountdown(3)
      setConfirmed(!requireCountdown) // immediately available when no countdown
      return
    }

    // No countdown required — button is immediately available
    if (!requireCountdown) {
      setConfirmed(true)
      return
    }

    if (countdown <= 0) {
      setConfirmed(true)
      return
    }

    const timer = setInterval(() => {
      setCountdown((prev) => {
        if (prev <= 1) {
          clearInterval(timer)
          return 0
        }
        return prev - 1
      })
    }, 1000)

    return () => clearInterval(timer)
  }, [open, countdown, requireCountdown])

  const handleConfirm = useCallback(() => {
    if (!confirmed) return
    setOpen(false)
    onConfirm()
  }, [confirmed, onConfirm])

  const handleCancel = useCallback(() => {
    setOpen(false)
  }, [])

  // Render description with **bold** markers
  const renderDescription = () => {
    const parts = description.split(/(\*\*.*?\*\*)/g)
    return parts.map((part, i) => {
      if (part.startsWith('**') && part.endsWith('**')) {
        return <strong key={i}>{part.slice(2, -2)}</strong>
      }
      return <span key={i}>{part}</span>
    })
  }

  return (
    <>
      <div onClick={() => setOpen(true)}>{children}</div>

      {open && (
        <div
          ref={dialogRef}
          className="fixed inset-0 z-50 flex items-center justify-center"
          role="dialog"
          aria-modal="true"
        >
          {/* Backdrop */}
          <div
            className="fixed inset-0 bg-black/50"
            onClick={handleCancel}
          />

          {/* Dialog */}
          <div className="relative z-10 w-full max-w-md mx-4 bg-background rounded-xl shadow-lg ring-1 ring-foreground/10 p-6">
            <h3 className="text-lg font-bold text-destructive mb-3">{title}</h3>
            <p className="text-sm text-muted-foreground mb-3 leading-relaxed">
              {renderDescription()}
            </p>
            {consequenceText && (
              <p className="text-sm text-destructive/80 mb-6 leading-relaxed font-medium">
                {consequenceText}
              </p>
            )}
            {!consequenceText && <div className="mb-3" />}
            <div className="flex justify-end gap-3">
              <Button variant="outline" onClick={handleCancel}>
                取消
              </Button>
              <Button
                variant="destructive"
                disabled={!confirmed}
                onClick={handleConfirm}
              >
                {confirmed
                  ? confirmText
                  : requireCountdown
                    ? `${confirmText} (${countdown}s)`
                    : confirmText}
              </Button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
