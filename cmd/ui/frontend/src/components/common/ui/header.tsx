import * as React from 'react'
import { cn } from '@/lib/utils'

interface HeaderProps extends React.HTMLAttributes<HTMLElement> {
  children: React.ReactNode
  variant?: 'default' | 'bordered' | 'elevated'
  size?: 'sm' | 'md' | 'lg'
  sticky?: boolean
}

const Header = React.forwardRef<HTMLElement, HeaderProps>(
  ({ className, children, variant = 'default', size = 'md', sticky = false, ...props }, ref) => {
    return (
      <header
        ref={ref}
        className={cn(
          'w-full flex items-center justify-between px-0 py-3 transition-all duration-200',
          {
            // Variants
            'bg-background dark:bg-card': variant === 'default',
            'bg-background dark:bg-card border-b border-border': variant === 'bordered',
            'bg-background dark:bg-card shadow-sm': variant === 'elevated',

            // Sizes
            'h-12 py-2': size === 'sm',
            'h-16 py-3': size === 'md',
            'h-20 py-4': size === 'lg',

            // Sticky positioning
            'sticky top-0 z-50': sticky,
          },
          className
        )}
        {...props}
      >
        {children}
      </header>
    )
  }
)
Header.displayName = 'Header'

// Header section components for better organization
interface HeaderSectionProps extends React.HTMLAttributes<HTMLDivElement> {
  children: React.ReactNode
  position?: 'left' | 'center' | 'right'
}

const HeaderSection = React.forwardRef<HTMLDivElement, HeaderSectionProps>(
  ({ className, children, position = 'left', ...props }, ref) => {
    return (
      <div
        ref={ref}
        className={cn(
          'flex items-center gap-2 px-4',
          {
            'justify-start': position === 'left',
            'justify-center': position === 'center',
            'justify-end': position === 'right',
          },
          className
        )}
        {...props}
      >
        {children}
      </div>
    )
  }
)
HeaderSection.displayName = 'HeaderSection'

// Header title component
interface HeaderTitleProps extends React.HTMLAttributes<HTMLHeadingElement> {
  children: React.ReactNode
  as?: 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'
}

const HeaderTitle = React.forwardRef<HTMLHeadingElement, HeaderTitleProps>(
  ({ className, children, as: Component = 'h1', ...props }, ref) => {
    return (
      <Component
        ref={ref}
        className={cn(
          'font-semibold text-foreground truncate',
          {
            'text-lg': Component === 'h1',
            'text-base': Component === 'h2',
            'text-sm': Component === 'h3',
            'text-xs': Component === 'h4',
          },
          className
        )}
        {...props}
      >
        {children}
      </Component>
    )
  }
)
HeaderTitle.displayName = 'HeaderTitle'

// Header subtitle component
interface HeaderSubtitleProps extends React.HTMLAttributes<HTMLParagraphElement> {
  children: React.ReactNode
}

const HeaderSubtitle = React.forwardRef<HTMLParagraphElement, HeaderSubtitleProps>(
  ({ className, children, ...props }, ref) => {
    return (
      <p
        ref={ref}
        className={cn('text-sm text-muted-foreground truncate', className)}
        {...props}
      >
        {children}
      </p>
    )
  }
)
HeaderSubtitle.displayName = 'HeaderSubtitle'

export { Header, HeaderSection, HeaderTitle, HeaderSubtitle }
export type { HeaderProps, HeaderSectionProps, HeaderTitleProps, HeaderSubtitleProps }
