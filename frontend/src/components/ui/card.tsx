import * as React from 'react'
import { cn } from '@/lib/utils'

const Card = React.forwardRef<HTMLDivElement, React.ComponentProps<'div'> & { size?: 'default' | 'sm' }>(
  function Card({ className, size = 'default', ...props }, ref) {
    return <div ref={ref} data-slot="card" data-size={size} className={cn('flex flex-col gap-5 border bg-card py-6 text-card-foreground', size === 'sm' && 'gap-4 py-4', className)} {...props} />
  },
)
function CardHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="card-header" className={cn('grid auto-rows-min items-start gap-2 px-6', className)} {...props} />
}
function CardTitle({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="card-title" className={cn('text-lg font-bold leading-snug', className)} {...props} />
}
function CardDescription({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="card-description" className={cn('text-sm leading-relaxed text-muted-foreground', className)} {...props} />
}
function CardAction({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="card-action" className={cn('col-start-2 row-start-1 row-span-2 self-start justify-self-end', className)} {...props} />
}
function CardContent({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="card-content" className={cn('px-6', className)} {...props} />
}
function CardFooter({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="card-footer" className={cn('flex items-center border-t bg-muted/40 px-6 py-4', className)} {...props} />
}
export { Card, CardHeader, CardFooter, CardTitle, CardAction, CardDescription, CardContent }
