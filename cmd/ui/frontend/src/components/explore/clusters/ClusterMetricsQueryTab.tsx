import { Button } from '@/components/common/ui/button'
import type { MetricQueryInfo } from '@/types/api'

interface ClusterMetricsQueryTabProps {
  queryInfo: MetricQueryInfo[] | undefined
}

export const ClusterMetricsQueryTab = ({ queryInfo }: ClusterMetricsQueryTabProps) => {
  if (!queryInfo || queryInfo.length === 0) {
    return (
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-8 text-center">
        <p className="text-gray-500 dark:text-gray-400">
          No query information available. Re-run{' '}
          <code className="px-1.5 py-0.5 rounded bg-gray-100 dark:bg-gray-800 text-sm">
            kcp discover
          </code>{' '}
          to generate metrics query details.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {(queryInfo ?? []).map((info, index) => (
        <div
          key={`${info.metric_name}-${index}`}
          className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6"
        >
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
            {info.metric_name}
          </h3>

          {/* Query Parameters */}
          <div className="grid grid-cols-2 gap-x-8 gap-y-3 mb-4">
            <div>
              <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                Namespace
              </span>
              <p className="text-sm text-gray-600 dark:text-gray-400">{info.namespace}</p>
            </div>
            <div>
              <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                Statistic
              </span>
              <p className="text-sm text-gray-600 dark:text-gray-400">{info.statistic}</p>
            </div>
            <div>
              <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                Dimensions
              </span>
              <div className="mt-1 flex flex-wrap gap-1.5">
                {info.dimensions.split(', ').map((dim) => (
                  <span
                    key={dim}
                    className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200"
                  >
                    {dim}
                  </span>
                ))}
              </div>
            </div>
            <div>
              <span className="text-sm font-medium text-gray-700 dark:text-gray-300">Period</span>
              <p className="text-sm text-gray-600 dark:text-gray-400">{info.period} seconds</p>
            </div>
          </div>

          {/* SEARCH Expression */}
          {info.search_expression && (
            <div className="mb-4">
              <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                SEARCH Expression
              </span>
              <pre className="mt-1 text-xs font-mono text-gray-800 dark:text-gray-200 overflow-auto bg-gray-50 dark:bg-gray-900 p-3 rounded border">
                {info.search_expression}
              </pre>
            </div>
          )}

          {/* Math Expression */}
          {info.math_expression && (
            <div className="mb-4">
              <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                Aggregation
              </span>
              <p className="text-sm text-gray-600 dark:text-gray-400">
                <code className="px-1.5 py-0.5 rounded bg-gray-100 dark:bg-gray-800 text-xs">
                  {info.math_expression}
                </code>
              </p>
            </div>
          )}

          {/* AWS CLI Command */}
          {info.aws_cli_command && (
            <div className="mb-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                  AWS CLI Command
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => navigator.clipboard.writeText(info.aws_cli_command)}
                  className="text-xs"
                >
                  Copy
                </Button>
              </div>
              <pre className="text-xs font-mono text-gray-800 dark:text-gray-200 overflow-auto max-h-48 bg-gray-50 dark:bg-gray-900 p-3 rounded border">
                {info.aws_cli_command}
              </pre>
            </div>
          )}

          {/* Aggregation Note */}
          <p className="text-xs text-gray-500 dark:text-gray-400 italic">
            {info.aggregation_note}
          </p>
        </div>
      ))}
    </div>
  )
}
