import { Button } from '@/components/ui/button'

interface AggregateStatRowProps {
  type: 'min' | 'avg' | 'max'
  value: number | null | undefined
  inModal: boolean
  onTransfer: (value: number, statType: 'min' | 'avg' | 'max') => void
  transferSuccess: string | null
  tcoField: string
}

function AggregateStatRow({
  type,
  value,
  inModal,
  onTransfer,
  transferSuccess,
  tcoField,
}: AggregateStatRowProps) {
  const typeLabels = {
    min: 'MIN',
    avg: 'AVG',
    max: 'MAX',
  }

  const typeColors = {
    min: 'text-blue-600 dark:text-blue-400',
    avg: 'text-green-600 dark:text-green-400',
    max: 'text-red-600 dark:text-red-400',
  }

  return (
    <div className="flex items-center justify-between">
      <div className="flex items-center gap-3">
        <span className="text-xs font-medium text-gray-700 dark:text-gray-300 uppercase w-8">
          {typeLabels[type]}
        </span>
        <span className={`text-sm font-semibold ${typeColors[type]}`}>
          {value?.toFixed(2) ?? 'N/A'}
        </span>
      </div>
      <div className="ml-4">
        {inModal && value !== null && value !== undefined ? (
          <Button
            onClick={() => onTransfer(value, type)}
            variant="outline"
            size="sm"
            className="h-6 w-36 text-xs"
          >
            <span className="flex items-center justify-center gap-1">
              {transferSuccess?.includes(`${tcoField}-${type}`) && (
                <span className="text-green-600">✓</span>
              )}
              Use as TCO Input
            </span>
          </Button>
        ) : (
          <div className="w-36"></div>
        )}
      </div>
    </div>
  )
}

interface MetricsAggregateStatsProps {
  aggregates: Record<string, { min?: number; avg?: number; max?: number }> | undefined
  selectedMetric: string
  inModal: boolean
  onTransfer: (value: number, statType: 'min' | 'avg' | 'max') => void
  transferSuccess: string | null
  tcoField: string
}

export default function MetricsAggregateStats({
  aggregates,
  selectedMetric,
  inModal,
  onTransfer,
  transferSuccess,
  tcoField,
}: MetricsAggregateStatsProps) {
  if (!aggregates || !selectedMetric) {
    return null
  }

  const metricAggregate = aggregates[selectedMetric]

  if (!metricAggregate) {
    return (
      <span className="text-sm text-gray-500 dark:text-gray-400">No data available</span>
    )
  }

  return (
    <div className="space-y-1">
      <AggregateStatRow
        type="min"
        value={metricAggregate.min}
        inModal={inModal}
        onTransfer={onTransfer}
        transferSuccess={transferSuccess}
        tcoField={tcoField}
      />
      <AggregateStatRow
        type="avg"
        value={metricAggregate.avg}
        inModal={inModal}
        onTransfer={onTransfer}
        transferSuccess={transferSuccess}
        tcoField={tcoField}
      />
      <AggregateStatRow
        type="max"
        value={metricAggregate.max}
        inModal={inModal}
        onTransfer={onTransfer}
        transferSuccess={transferSuccess}
        tcoField={tcoField}
      />
    </div>
  )
}

