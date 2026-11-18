import { Button as ButtonPrimitive } from '@base-ui/react/button'
import { forwardRef } from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'ef-button inline-flex shrink-0 items-center justify-center gap-2 border border-transparent text-sm font-semibold whitespace-nowrap select-none disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        default: 'bg-primary text-primary-foreground',
        outline: 'border-input bg-card text-foreground hover:bg-muted aria-expanded:bg-muted',
        secondary: 'bg-secondary text-secondary-foreground hover:bg-accent hover:text-accent-foreground',
        ghost: 'bg-transparent text-foreground hover:bg-muted aria-expanded:bg-muted',
        destructive: 'bg-destructive/10 text-destructive hover:bg-destructive/20',
        link: 'bg-transparent text-foreground underline underline-offset-4 hover:bg-muted',
      },
      size: {
        default: 'min-h-[44px] px-5 py-2.5',
        xs: 'min-h-[44px] px-3 py-2 text-xs',
        sm: 'min-h-[44px] px-4 py-2 text-sm',
        lg: 'min-h-[48px] px-6 py-3 text-base',
        icon: 'h-11 w-11',
        'icon-xs': 'h-11 w-11',
        'icon-sm': 'h-11 w-11',
        'icon-lg': 'h-12 w-12',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
)
const Button = forwardRef<HTMLButtonElement, ButtonPrimitive.Props & VariantProps<typeof buttonVariants>>(
  function Button({ className, variant = 'default', size = 'default', ...props }, ref) {
    return <ButtonPrimitive ref={ref} data-slot="button" data-variant={variant} className={cn(buttonVariants({ variant, size, className }))} {...props} />
  },
)
export { Button, buttonVariants }
