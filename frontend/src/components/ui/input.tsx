import * as React from 'react'
import { Input as InputPrimitive } from '@base-ui/react/input'
import { cn } from '@/lib/utils'
function Input({ className, type, ...props }: React.ComponentProps<'input'>) {
  return <InputPrimitive type={type} data-slot="input" className={cn(
    'min-h-[48px] w-full min-w-0 border border-input bg-card px-3 py-2.5 text-base text-foreground transition-colors placeholder:text-muted-foreground focus:border-foreground disabled:cursor-not-allowed disabled:bg-muted disabled:opacity-60 file:mr-3 file:border-0 file:bg-muted file:px-3 file:py-2 file:text-sm file:font-medium file:text-foreground aria-invalid:border-foreground aria-invalid:bg-accent/10',
    className,
  )} {...props} />
}
export { Input }
