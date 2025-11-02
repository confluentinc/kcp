interface StatusBadgeProps {
  enabled: boolean
  label: string
  className?: string
}

export default function StatusBadge({ enabled, label, className = '' }: StatusBadgeProps) {
  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 text-xs font-medium rounded bg-gray-100 dark:bg-gray-800 border border-gray-200 dark:border-border ${
        enabled ? 'text-green-300 dark:text-green-400' : 'text-red-300 dark:text-red-400'
      } ${className}`}
    >
      {label}
    </span>
  )
}
