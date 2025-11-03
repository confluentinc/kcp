interface BooleanStatusProps {
  value: boolean | undefined
  trueLabel?: string
  falseLabel?: string
  unknownLabel?: string
  useBadge?: boolean // Kept for backwards compatibility but now always uses badge styling
}

export const BooleanStatus = ({
  value,
  trueLabel = 'Enabled',
  falseLabel = 'Disabled',
  unknownLabel = 'Unknown',
}: BooleanStatusProps) => {
  if (value === undefined) {
    return <span className="text-gray-500 dark:text-gray-400">{unknownLabel}</span>
  }

  // All badges now use the same gray background with pastel colored text
  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 text-xs font-medium rounded bg-gray-100 dark:bg-gray-800 border border-gray-200 dark:border-border ${
        value ? 'text-green-300 dark:text-green-400' : 'text-red-300 dark:text-red-400'
      }`}
    >
      {value ? trueLabel : falseLabel}
    </span>
  )
}
