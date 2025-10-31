import KeyValuePair from './KeyValuePair'

interface KeyValueGridItem {
  label: string
  value: React.ReactNode
  labelClassName?: string
  valueClassName?: string
  alignItems?: 'center' | 'start' | 'end'
}

interface KeyValueGridProps {
  items: KeyValueGridItem[]
  columns?: 1 | 2 | 3
  className?: string
  itemClassName?: string
}

export default function KeyValueGrid({
  items,
  columns = 2,
  className = '',
  itemClassName = '',
}: KeyValueGridProps) {
  const gridColsClass = {
    1: 'grid-cols-1',
    2: 'grid-cols-1 md:grid-cols-2',
    3: 'grid-cols-1 md:grid-cols-2 lg:grid-cols-3',
  }[columns]

  return (
    <div className={`grid ${gridColsClass} gap-4 text-sm ${className}`}>
      {items.map((item, index) => (
        <KeyValuePair
          key={index}
          label={item.label}
          value={item.value}
          labelClassName={item.labelClassName}
          valueClassName={item.valueClassName}
          alignItems={item.alignItems}
          className={itemClassName}
        />
      ))}
    </div>
  )
}

