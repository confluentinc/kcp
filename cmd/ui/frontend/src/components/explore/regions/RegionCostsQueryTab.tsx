import { ExternalLink } from 'lucide-react'
import { Button } from '@/components/common/ui/button'
import type { CostQueryInfo } from '@/types/api'

interface RegionCostsQueryTabProps {
  queryInfo: CostQueryInfo | undefined
}

export const RegionCostsQueryTab = ({ queryInfo }: RegionCostsQueryTabProps) => {
  if (!queryInfo || !queryInfo.aws_cli_command) {
    return (
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-8 text-center">
        <p className="text-gray-500 dark:text-gray-400">
          No query information available. Re-run <code className="px-1.5 py-0.5 rounded bg-gray-100 dark:bg-gray-800 text-sm">kcp discover</code> to generate cost query details.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Query Parameters Section */}
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
          Query Parameters
        </h3>
        <div className="grid grid-cols-2 gap-x-8 gap-y-3">
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Time Range
            </span>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              {queryInfo.time_period.start} to {queryInfo.time_period.end}
            </p>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Granularity
            </span>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              {queryInfo.granularity}
            </p>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Regions
            </span>
            <div className="mt-1 flex flex-wrap gap-1.5">
              {(queryInfo.regions ?? []).map((region) => (
                <span
                  key={region}
                  className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200"
                >
                  {region}
                </span>
              ))}
            </div>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Group By
            </span>
            <div className="mt-1 flex flex-wrap gap-1.5">
              {(queryInfo.group_by ?? []).map((group) => (
                <span
                  key={group}
                  className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200"
                >
                  {group}
                </span>
              ))}
            </div>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Services
            </span>
            <div className="mt-1 flex flex-wrap gap-1.5">
              {(queryInfo.services ?? []).map((service) => (
                <span
                  key={service}
                  className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200"
                >
                  {service}
                </span>
              ))}
            </div>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Metrics
            </span>
            <div className="mt-1 flex flex-wrap gap-1.5">
              {(queryInfo.metrics ?? []).map((metric) => (
                <span
                  key={metric}
                  className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300"
                >
                  {metric}
                </span>
              ))}
            </div>
          </div>
          {queryInfo.tags && Object.keys(queryInfo.tags).length > 0 && (
            <div className="col-span-2">
              <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                Tags
              </span>
              <div className="mt-1 flex flex-wrap gap-1.5">
                {Object.entries(queryInfo.tags).map(([key, values]) => (
                  <span
                    key={key}
                    className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200"
                  >
                    {key}: {values.join(', ')}
                  </span>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* AWS CLI Command Section */}
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            AWS CLI Command
          </h3>
          <Button
            variant="outline"
            size="sm"
            onClick={() => navigator.clipboard.writeText(queryInfo.aws_cli_command)}
            className="text-xs"
          >
            Copy CLI Command
          </Button>
        </div>
        <pre className="text-xs font-mono text-gray-800 dark:text-gray-200 overflow-auto max-h-96 bg-gray-50 dark:bg-gray-900 p-4 rounded border">
          {queryInfo.aws_cli_command}
        </pre>
      </div>

      {/* AWS Console Link Section */}
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
          AWS Console
        </h3>
        <Button
          variant="outline"
          onClick={() => window.open(queryInfo.console_url, '_blank')}
          className="flex items-center gap-2"
        >
          <ExternalLink className="h-4 w-4" />
          Open in AWS Cost Explorer
        </Button>
      </div>

      {/* Aggregation Note Section */}
      <div className="bg-blue-50 dark:bg-blue-950/20 rounded-lg border border-blue-200 dark:border-blue-800 p-6">
        <h3 className="text-lg font-semibold text-blue-900 dark:text-blue-100 mb-2">
          Data Processing Note
        </h3>
        <p className="text-sm text-blue-800 dark:text-blue-200">{queryInfo.aggregation_note}</p>
      </div>
    </div>
  )
}
