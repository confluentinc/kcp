import { useState } from 'react'
import { Button } from '@/components/common/ui/button'
import type { MetricQueryInfo } from '@/types/api'

interface ClusterMetricsQueryTabProps {
  queryInfo: MetricQueryInfo[] | undefined
}

export const ClusterMetricsQueryTab = ({ queryInfo }: ClusterMetricsQueryTabProps) => {
  if (!queryInfo || queryInfo.length === 0) {
    return (
      <div className="bg-card rounded-lg border border-border p-8 text-center">
        <p className="text-muted-foreground">
          No query information available. Re-run{' '}
          <code className="px-1.5 py-0.5 rounded bg-secondary text-sm">
            kcp discover
          </code>{' '}
          to generate metrics query details.
        </p>
      </div>
    )
  }

  const [copiedKey, setCopiedKey] = useState<string | null>(null)

  const handleCopy = (text: string, key: string) => {
    navigator.clipboard.writeText(text)
    setCopiedKey(key)
    setTimeout(() => setCopiedKey(null), 2000)
  }

  return (
    <div className="space-y-6">
      {queryInfo.map((info, index) => (
        <div
          key={`${info.metric_name}-${index}`}
          className="bg-card rounded-lg border border-border p-6"
        >
          <h3 className="text-lg font-semibold text-foreground mb-4">
            {info.metric_name}
          </h3>

          {/* Query Parameters */}
          <div className="grid grid-cols-2 gap-x-8 gap-y-3 mb-4">
            <div>
              <span className="text-sm font-medium text-foreground">
                Namespace
              </span>
              <p className="text-sm text-muted-foreground">{info.namespace}</p>
            </div>
            <div>
              <span className="text-sm font-medium text-foreground">
                Statistic
              </span>
              <p className="text-sm text-muted-foreground">{info.statistic}</p>
            </div>
            <div>
              <span className="text-sm font-medium text-foreground">
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
              <span className="text-sm font-medium text-foreground">Period</span>
              <p className="text-sm text-muted-foreground">{info.period} seconds</p>
            </div>
          </div>

          {/* Math Expression */}
          {info.math_expression && (
            <div className="mb-4">
              <span className="text-sm font-medium text-foreground">
                Aggregation
              </span>
              <p className="text-sm text-muted-foreground">
                <code className="px-1.5 py-0.5 rounded bg-secondary text-xs">
                  {info.math_expression}
                </code>
              </p>
            </div>
          )}

          {/* CloudWatch Console Source JSON */}
          {info.console_source_json && (
            <div className="mb-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-sm font-medium text-foreground">
                  CloudWatch Console Source JSON
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => handleCopy(info.console_source_json, `console-${index}`)}
                  className="text-xs"
                >
                  {copiedKey === `console-${index}` ? 'Copied!' : 'Copy'}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground mb-2">
                Paste into CloudWatch &rarr; Metrics &rarr; All metrics &rarr;{' '}
                <strong>Source</strong> tab, then click <strong>Update</strong>.
              </p>
              <pre className="text-xs font-mono text-foreground overflow-auto max-h-48 bg-secondary p-3 rounded border">
                {info.console_source_json}
              </pre>
            </div>
          )}

          {/* AWS CLI Command */}
          {info.aws_cli_command && (
            <div className="mb-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-sm font-medium text-foreground">
                  AWS CLI Command
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => handleCopy(info.aws_cli_command, `cli-${index}`)}
                  className="text-xs"
                >
                  {copiedKey === `cli-${index}` ? 'Copied!' : 'Copy'}
                </Button>
              </div>
              <pre className="text-xs font-mono text-foreground overflow-auto max-h-48 bg-secondary p-3 rounded border">
                {info.aws_cli_command}
              </pre>
            </div>
          )}

          {/* Aggregation Note */}
          <p className="text-xs text-muted-foreground italic">
            {info.aggregation_note}
          </p>
        </div>
      ))}
    </div>
  )
}
