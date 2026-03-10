import { ExternalLink } from 'lucide-react'
import { MetricsCodeViewer } from '@/components/explore/clusters/MetricsCodeViewer'
import { Button } from '@/components/common/ui/button'
import type { CostQueryInfo } from '@/types/api'

interface RegionCostsQueryTabProps {
  queryInfo: CostQueryInfo | undefined
}

export const RegionCostsQueryTab = ({ queryInfo }: RegionCostsQueryTabProps) => {
  if (!queryInfo) {
    return (
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-8 text-center">
        <p className="text-gray-500 dark:text-gray-400">No query information available</p>
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
        <div className="space-y-3">
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Time Range:
            </span>
            <span className="ml-2 text-sm text-gray-600 dark:text-gray-400">
              {queryInfo.time_period.start} to {queryInfo.time_period.end}
            </span>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Granularity:
            </span>
            <span className="ml-2 text-sm text-gray-600 dark:text-gray-400">
              {queryInfo.granularity}
            </span>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Services:
            </span>
            <div className="mt-2 flex flex-wrap gap-2">
              {queryInfo.services.map((service) => (
                <span
                  key={service}
                  className="inline-flex items-center px-2.5 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200"
                >
                  {service}
                </span>
              ))}
            </div>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Regions:
            </span>
            <div className="mt-2 flex flex-wrap gap-2">
              {queryInfo.regions.map((region) => (
                <span
                  key={region}
                  className="inline-flex items-center px-2.5 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200"
                >
                  {region}
                </span>
              ))}
            </div>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Group By:
            </span>
            <div className="mt-2 flex flex-wrap gap-2">
              {queryInfo.group_by.map((group) => (
                <span
                  key={group}
                  className="inline-flex items-center px-2.5 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200"
                >
                  {group}
                </span>
              ))}
            </div>
          </div>
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Metrics:
            </span>
            <ul className="mt-2 ml-6 list-disc text-sm text-gray-600 dark:text-gray-400">
              {queryInfo.metrics.map((metric) => (
                <li key={metric}>{metric}</li>
              ))}
            </ul>
          </div>
          {queryInfo.tags && Object.keys(queryInfo.tags).length > 0 && (
            <div>
              <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                Tags:
              </span>
              <div className="mt-2 space-y-1">
                {Object.entries(queryInfo.tags).map(([key, values]) => (
                  <div
                    key={key}
                    className="text-sm text-gray-600 dark:text-gray-400"
                  >
                    <span className="font-medium">{key}:</span> {values.join(', ')}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* AWS CLI Command Section */}
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
          AWS CLI Command
        </h3>
        <MetricsCodeViewer
          data={queryInfo.aws_cli_command}
          label="CLI Command"
          onCopy={() => navigator.clipboard.writeText(queryInfo.aws_cli_command)}
          isJSON={false}
        />
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
