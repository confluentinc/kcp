interface KeyValuePairProps {
  label: string
  value: React.ReactNode
  className?: string
  labelClassName?: string
  valueClassName?: string
  alignItems?: 'center' | 'start' | 'end'
}

export default function KeyValuePair({
  label,
  value,
  className = '',
  labelClassName = 'text-gray-600 dark:text-gray-400',
  valueClassName = 'font-medium text-gray-900 dark:text-gray-100',
  alignItems = 'start',
}: KeyValuePairProps) {
  const alignItemsClass = `items-${alignItems}`
  return (
    <div className={`flex justify-between ${alignItemsClass} ${className}`}>
      <span className={labelClassName}>{label}</span>
      <span className={valueClassName}>{value}</span>
    </div>
  )
}

