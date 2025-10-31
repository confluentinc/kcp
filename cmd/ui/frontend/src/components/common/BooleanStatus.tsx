interface BooleanStatusProps {
  value: boolean | undefined
  trueLabel?: string
  falseLabel?: string
  unknownLabel?: string
}

export default function BooleanStatus({
  value,
  trueLabel = '✓',
  falseLabel = '✗',
  unknownLabel = 'Unknown',
}: BooleanStatusProps) {
  if (value === undefined) {
    return <span className="text-gray-500 dark:text-gray-400">{unknownLabel}</span>
  }

  return (
    <span
      className={`${
        value ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'
      }`}
    >
      {value ? trueLabel : falseLabel}
    </span>
  )
}

