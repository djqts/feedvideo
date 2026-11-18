import { useEffect } from 'react'

function getFocusableElements(container: HTMLElement): HTMLElement[] {
  const selector = [
    'a[href]',
    'button:not([disabled])',
    'input:not([disabled])',
    'textarea:not([disabled])',
    'select:not([disabled])',
    '[tabindex]:not([tabindex="-1"])',
  ].join(',')
  return Array.from(container.querySelectorAll<HTMLElement>(selector)).filter(
    (el) => !el.hasAttribute('disabled'),
  )
}

export function useFocusTrap(
  active: boolean,
  containerRef: React.RefObject<HTMLElement>,
  triggerRef?: React.RefObject<HTMLElement>,
  onClose?: () => void,
) {
  useEffect(() => {
    if (!active || !containerRef.current) return

    const container = containerRef.current
    const focusable = getFocusableElements(container)
    const firstFocusable = focusable[0]

    // Focus first element when trap activates
    if (firstFocusable) {
      firstFocusable.focus()
    }

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose?.()
        return
      }

      if (e.key !== 'Tab') return

      const currentFocusables = getFocusableElements(container)
      if (currentFocusables.length === 0) {
        e.preventDefault()
        return
      }

      const first = currentFocusables[0]
      const last = currentFocusables[currentFocusables.length - 1]
      const currentIndex = currentFocusables.indexOf(document.activeElement as HTMLElement)

      if (e.shiftKey) {
        if (currentIndex <= 0 || !container.contains(document.activeElement)) {
          e.preventDefault()
          last.focus()
        } else {
          e.preventDefault()
          currentFocusables[currentIndex - 1]?.focus()
        }
      } else {
        if (currentIndex >= currentFocusables.length - 1 || !container.contains(document.activeElement)) {
          e.preventDefault()
          first.focus()
        } else {
          e.preventDefault()
          currentFocusables[currentIndex + 1]?.focus()
        }
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [active, containerRef, onClose])

  // Restore focus to trigger when deactivated
  useEffect(() => {
    if (active) return
    if (triggerRef?.current) {
      triggerRef.current.focus()
    }
  }, [active, triggerRef])
}
