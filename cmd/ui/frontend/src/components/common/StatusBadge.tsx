interface StatusBadgeProps {
  enabled: boolean
  label: string
  className?: string
}

export const StatusBadge = ({ enabled, label, className = '' }: StatusBadgeProps) => {
  return (
    <span
      className={`inline-flex items-center px-2.5 py-0.5 text-xs font-medium rounded-full ${
        enabled
          ? 'bg-success/10 text-success border border-success/20'
          : 'bg-muted text-muted-foreground border border-border'
      } ${className}`}
    >
      {label}
    </span>
  )
}
