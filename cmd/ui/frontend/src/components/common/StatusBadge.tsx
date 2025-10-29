/**
 * StatusBadge component
 * Unified status badge component with consistent styling across the application
 */

interface StatusBadgeProps {
  enabled: boolean
  label: string
  className?: string
}

export default function StatusBadge({ enabled, label, className = '' }: StatusBadgeProps) {
  const baseClasses = 'text-sm font-medium'
  const statusClasses = enabled
    ? 'text-green-600 dark:text-green-400'
    : 'text-red-600 dark:text-red-400'

  return (
    <span className={`${baseClasses} ${statusClasses} ${className}`}>
      {label}
    </span>
  )
}

/**
 * StatusBadge with checkmark/cross indicator
 * Shows ✓ or ✗ based on enabled status
 */
export function StatusBadgeWithIndicator({ enabled, label }: StatusBadgeProps) {
  const baseClasses = 'text-sm font-medium'
  const statusClasses = enabled
    ? 'text-green-600 dark:text-green-400'
    : 'text-red-600 dark:text-red-400'
  const indicator = enabled ? '✓' : '✗'

  return (
    <span className={`${baseClasses} ${statusClasses}`}>
      {indicator} {label}
    </span>
  )
}


