import React from 'react'
import { cn } from '../lib/utils'

interface Props { eyebrow: string; title: string; description?: string; children?: React.ReactNode; className?: string }

export default function PageHeader({ eyebrow, title, description, children, className }: Props) {
  return <header className={cn('ef-page-header', className)}>
    <div className="min-w-0">
      <div className="ef-eyebrow mb-3">// {eyebrow}</div>
      <h1>{title}</h1>
      {description && <p>{description}</p>}
    </div>
    {children && <div className="flex flex-wrap items-center gap-3">{children}</div>}
  </header>
}
