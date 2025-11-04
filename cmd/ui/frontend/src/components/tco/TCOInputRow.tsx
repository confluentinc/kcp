import { ExternalLink } from 'lucide-react'
import { Button } from '@/components/common/ui/button'
import { findClusterInRegions } from '@/lib/clusterUtils'
import type { Region, Cluster } from '@/types'

interface TCOCluster {
  name: string
  regionName: string
  arn: string
  key: string
}

interface TCOWorkloadData {
  [clusterKey: string]: {
    avgIngressThroughput?: string
    peakIngressThroughput?: string
    avgEgressThroughput?: string
    peakEgressThroughput?: string
    retentionDays?: string
    partitions?: string
    replicationFactor?: string
    localRetentionHours?: string
  }
}

interface TCOInputRowProps {
  label: string
  clusters: TCOCluster[]
  tcoWorkloadData: TCOWorkloadData
  regions: Region[]
  field?: string
  readOnly?: boolean
  readOnlyValue?: (cluster: Cluster | undefined) => boolean | undefined
  onInputChange?: (
    clusterKey: string,
    field:
      | 'avgIngressThroughput'
      | 'peakIngressThroughput'
      | 'avgEgressThroughput'
      | 'peakEgressThroughput'
      | 'retentionDays'
      | 'partitions'
      | 'replicationFactor'
      | 'localRetentionHours',
    value: string
  ) => void
  onMetricsClick?: (clusterKey: string, metricType: string) => void
  metricType?: string
  inputType?: 'number'
  step?: string
  min?: string
  placeholder?: string
  buttonDisabled?: boolean
  buttonTitle?: string
}

/**
 * Reusable row component for TCO input table
 * Handles both input fields and read-only display fields
 */
export const TCOInputRow = ({
  label,
  clusters,
  tcoWorkloadData,
  regions,
  field,
  readOnly = false,
  readOnlyValue,
  onInputChange,
  onMetricsClick,
  metricType,
  inputType = 'number',
  step,
  min,
  placeholder,
  buttonDisabled = false,
  buttonTitle,
}: TCOInputRowProps) => {
  const inputClassName =
    'flex-1 px-3 py-2 border border-gray-300 dark:border-border rounded-md text-sm bg-white dark:bg-card text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-blue-500 focus:border-blue-500 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none'

  if (readOnly && readOnlyValue) {
    return (
      <tr>
        <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
          {label}
        </td>
        {clusters.map((cluster) => {
          const clusterObj = findClusterInRegions(regions, cluster.regionName, cluster.name)
          const value = readOnlyValue(clusterObj)

          return (
            <td
              key={cluster.key}
              className="px-4 py-3"
            >
              <div className="flex justify-center">
                {value !== undefined ? (
                  <span
                    className={`inline-flex items-center justify-center w-6 h-6 rounded-full text-sm font-medium ${
                      value
                        ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                        : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                    }`}
                  >
                    {value ? '✓' : '✗'}
                  </span>
                ) : (
                  <span className="text-sm text-gray-500 dark:text-gray-400">N/A</span>
                )}
              </div>
            </td>
          )
        })}
      </tr>
    )
  }

  if (!field || !onInputChange) {
    return null
  }

  return (
    <tr>
      <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 bg-gray-50 dark:bg-card">
        {label}
      </td>
      {clusters.map((cluster) => (
        <td
          key={cluster.key}
          className="px-4 py-3"
        >
          <div className="flex items-center gap-2">
            <input
              type={inputType}
              step={step}
              min={min}
              value={
                (tcoWorkloadData[cluster.key]?.[field as keyof TCOWorkloadData[string]] as
                  | string
                  | undefined) || ''
              }
              onChange={(e) =>
                onInputChange(
                  cluster.key,
                  field as
                    | 'avgIngressThroughput'
                    | 'peakIngressThroughput'
                    | 'avgEgressThroughput'
                    | 'peakEgressThroughput'
                    | 'retentionDays'
                    | 'partitions'
                    | 'replicationFactor'
                    | 'localRetentionHours',
                  e.target.value
                )
              }
              className={inputClassName}
              placeholder={placeholder}
            />
            {onMetricsClick && metricType && (
              <Button
                onClick={() => onMetricsClick(cluster.key, metricType)}
                disabled={buttonDisabled}
                variant="outline"
                size="sm"
                className={`h-8 w-8 p-0 flex-shrink-0 ${
                  buttonDisabled ? 'opacity-50 cursor-not-allowed' : ''
                }`}
                title={buttonTitle || 'Go to cluster metrics'}
              >
                <ExternalLink className="h-3 w-3" />
              </Button>
            )}
          </div>
        </td>
      ))}
    </tr>
  )
}
