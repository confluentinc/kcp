interface StatusBadgeProps {
  enabled: boolean
  label: string
  className?: string
  showIndicator?: boolean
}

export default function StatusBadge({
  enabled,
  label,
  className = '',
  showIndicator = false,
}: StatusBadgeProps) {
  const baseClasses = 'text-sm font-medium'
  const statusClasses = enabled
    ? 'text-green-600 dark:text-green-400'
    : 'text-red-600 dark:text-red-400'
  const indicator = showIndicator ? (enabled ? '✓' : '✗') : null

  return (
    <span className={`${baseClasses} ${statusClasses} ${className}`}>
      {indicator && `${indicator} `}
      {label}
    </span>
  )
}
