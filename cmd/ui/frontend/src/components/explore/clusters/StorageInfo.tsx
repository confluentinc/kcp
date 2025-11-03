import { KeyValuePair } from '@/components/common/KeyValuePair'
import { StatusBadge } from '@/components/common/StatusBadge'
import { createStatusBadgeProps } from '@/lib/utils'

interface StorageInfoProps {
  volumeSize: number
  brokerNodes: number
  provisionedThroughput?: {
    Enabled: boolean
  }
  displayMode?: 'inline' | 'detailed'
}

export const StorageInfo = ({
  volumeSize,
  brokerNodes,
  provisionedThroughput,
  displayMode = 'inline',
}: StorageInfoProps) => {
  const totalStorage = volumeSize * brokerNodes

  if (displayMode === 'detailed') {
    return (
      <div className="bg-gray-50 dark:bg-card rounded-lg p-4 transition-colors">
        <div className="space-y-2 text-sm">
          <KeyValuePair
            label="Volume Size:"
            value={`${volumeSize} GB`}
            valueClassName="font-bold text-blue-600 dark:text-accent"
          />
          <KeyValuePair
            label="Total Storage:"
            value={`${totalStorage} GB`}
            valueClassName="font-bold text-green-600 dark:text-green-400"
          />
          {provisionedThroughput && (
            <KeyValuePair
              label="Provisioned Throughput:"
              value={<StatusBadge {...createStatusBadgeProps(provisionedThroughput.Enabled)} />}
            />
          )}
        </div>
      </div>
    )
  }

  // Inline mode for grid display
  return (
    <>
      <KeyValuePair
        label="Storage per Broker (GB):"
        value={volumeSize}
      />
      <KeyValuePair
        label="Total Storage (GB):"
        value={totalStorage}
      />
    </>
  )
}

