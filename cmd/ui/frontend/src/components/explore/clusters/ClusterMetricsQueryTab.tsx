import { useState } from 'react'
import { Button } from '@/components/common/ui/button'
import type { MetricQueryInfo } from '@/types/api'

interface ClusterMetricsQueryTabProps {
  queryInfo: MetricQueryInfo[] | undefined
}

function getSourceType(
  info: MetricQueryInfo
): 'cloudwatch' | 'jolokia' | 'prometheus' {
  if (info.source_type) return info.source_type
  if (info.namespace) return 'cloudwatch'
  if (info.mbean_path) return 'jolokia'
  if (info.promql_query) return 'prometheus'
  return 'cloudwatch'
}

function sourceLabel(
  sourceType: 'cloudwatch' | 'jolokia' | 'prometheus'
): string {
  switch (sourceType) {
    case 'cloudwatch':
      return 'CloudWatch'
    case 'jolokia':
      return 'Jolokia'
    case 'prometheus':
      return 'Prometheus'
  }
}

function sourceBadgeClasses(
  sourceType: 'cloudwatch' | 'jolokia' | 'prometheus'
): string {
  switch (sourceType) {
    case 'cloudwatch':
      return 'bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200'
    case 'jolokia':
      return 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
    case 'prometheus':
      return 'bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200'
  }
}

export const ClusterMetricsQueryTab = ({
  queryInfo,
}: ClusterMetricsQueryTabProps) => {
  const [copiedKey, setCopiedKey] = useState<string | null>(null)

  if (!queryInfo || queryInfo.length === 0) {
    return (
      <div className="bg-card rounded-lg border border-border p-8 text-center">
        <p className="text-muted-foreground">
          No query information available. Re-run{' '}
          <code className="px-1.5 py-0.5 rounded bg-secondary text-sm">
            kcp discover
          </code>{' '}
          or{' '}
          <code className="px-1.5 py-0.5 rounded bg-secondary text-sm">
            kcp scan clusters
          </code>{' '}
          to generate metrics query details.
        </p>
      </div>
    )
  }

  const handleCopy = (text: string, key: string) => {
    navigator.clipboard.writeText(text)
    setCopiedKey(key)
    setTimeout(() => setCopiedKey(null), 2000)
  }

  return (
    <div className="space-y-6">
      {queryInfo.map((info, index) => {
        const source = getSourceType(info)
        return (
          <div
            key={`${info.metric_name}-${index}`}
            className="bg-card rounded-lg border border-border p-6"
          >
            <div className="flex items-center gap-3 mb-4">
              <h3 className="text-lg font-semibold text-foreground">
                {info.metric_name}
              </h3>
              <span
                className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${sourceBadgeClasses(source)}`}
              >
                {sourceLabel(source)}
              </span>
            </div>

            {source === 'cloudwatch' && (
              <CloudWatchSection
                info={info}
                index={index}
                copiedKey={copiedKey}
                onCopy={handleCopy}
              />
            )}

            {source === 'jolokia' && (
              <JolokiaSection
                info={info}
                index={index}
                copiedKey={copiedKey}
                onCopy={handleCopy}
              />
            )}

            {source === 'prometheus' && (
              <PrometheusSection
                info={info}
                index={index}
                copiedKey={copiedKey}
                onCopy={handleCopy}
              />
            )}

            {/* Aggregation Note - shared across all sources */}
            <p className="text-xs text-muted-foreground italic">
              {info.aggregation_note}
            </p>
          </div>
        )
      })}
    </div>
  )
}

interface SectionProps {
  info: MetricQueryInfo
  index: number
  copiedKey: string | null
  onCopy: (text: string, key: string) => void
}

const CloudWatchSection = ({ info, index, copiedKey, onCopy }: SectionProps) => (
  <>
    {/* Query Parameters */}
    <div className="grid grid-cols-2 gap-x-8 gap-y-3 mb-4">
      <div>
        <span className="text-sm font-medium text-foreground">Namespace</span>
        <p className="text-sm text-muted-foreground">{info.namespace}</p>
      </div>
      <div>
        <span className="text-sm font-medium text-foreground">Statistic</span>
        <p className="text-sm text-muted-foreground">{info.statistic}</p>
      </div>
      <div>
        <span className="text-sm font-medium text-foreground">Dimensions</span>
        <div className="mt-1 flex flex-wrap gap-1.5">
          {info.dimensions?.split(', ').map((dim) => (
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
        <span className="text-sm font-medium text-foreground">Aggregation</span>
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
            onClick={() =>
              onCopy(info.console_source_json!, `console-${index}`)
            }
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
            onClick={() => onCopy(info.aws_cli_command!, `cli-${index}`)}
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
  </>
)

const JolokiaSection = ({ info, index, copiedKey, onCopy }: SectionProps) => (
  <>
    {/* Query Parameters */}
    <div className="grid grid-cols-2 gap-x-8 gap-y-3 mb-4">
      <div>
        <span className="text-sm font-medium text-foreground">Statistic</span>
        <p className="text-sm text-muted-foreground">{info.statistic}</p>
      </div>
      <div>
        <span className="text-sm font-medium text-foreground">
          Poll Interval
        </span>
        <p className="text-sm text-muted-foreground">{info.period} seconds</p>
      </div>
      <div>
        <span className="text-sm font-medium text-foreground">
          Query Duration
        </span>
        <p className="text-sm text-muted-foreground">{info.query_duration}</p>
      </div>
      <div>
        <span className="text-sm font-medium text-foreground">
          Jolokia Endpoint
        </span>
        <p className="text-sm text-muted-foreground">{info.jolokia_url}</p>
      </div>
    </div>

    {/* MBean Path */}
    {info.mbean_path && (
      <div className="mb-4">
        <span className="text-sm font-medium text-foreground">MBean Path</span>
        <pre className="mt-1 text-xs font-mono text-foreground overflow-auto bg-secondary p-3 rounded border">
          {info.mbean_path}
        </pre>
      </div>
    )}

    {/* Curl Command */}
    {info.curl_command && (
      <div className="mb-4">
        <div className="flex items-center justify-between mb-2">
          <span className="text-sm font-medium text-foreground">
            Curl Command
          </span>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onCopy(info.curl_command!, `curl-${index}`)}
            className="text-xs"
          >
            {copiedKey === `curl-${index}` ? 'Copied!' : 'Copy'}
          </Button>
        </div>
        <pre className="text-xs font-mono text-foreground overflow-auto max-h-48 bg-secondary p-3 rounded border">
          {info.curl_command}
        </pre>
      </div>
    )}
  </>
)

const PrometheusSection = ({ info, index, copiedKey, onCopy }: SectionProps) => (
  <>
    {/* Query Parameters */}
    <div className="grid grid-cols-2 gap-x-8 gap-y-3 mb-4">
      <div>
        <span className="text-sm font-medium text-foreground">Statistic</span>
        <p className="text-sm text-muted-foreground">{info.statistic}</p>
      </div>
      <div>
        <span className="text-sm font-medium text-foreground">Query Step</span>
        <p className="text-sm text-muted-foreground">{info.period} seconds</p>
      </div>
      <div>
        <span className="text-sm font-medium text-foreground">
          Query Duration
        </span>
        <p className="text-sm text-muted-foreground">{info.query_duration}</p>
      </div>
      <div>
        <span className="text-sm font-medium text-foreground">
          Prometheus URL
        </span>
        <p className="text-sm text-muted-foreground">{info.prometheus_url}</p>
      </div>
    </div>

    {/* Prometheus Metric Name */}
    {info.prometheus_metric_name && (
      <div className="mb-4">
        <span className="text-sm font-medium text-foreground">
          Prometheus Metric
        </span>
        <p className="text-sm text-muted-foreground">
          <code className="px-1.5 py-0.5 rounded bg-secondary text-xs">
            {info.prometheus_metric_name}
          </code>
        </p>
      </div>
    )}

    {/* PromQL Query */}
    {info.promql_query && (
      <div className="mb-4">
        <div className="flex items-center justify-between mb-2">
          <span className="text-sm font-medium text-foreground">
            PromQL Query
          </span>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onCopy(info.promql_query!, `promql-${index}`)}
            className="text-xs"
          >
            {copiedKey === `promql-${index}` ? 'Copied!' : 'Copy'}
          </Button>
        </div>
        <pre className="mt-1 text-xs font-mono text-foreground overflow-auto bg-secondary p-3 rounded border">
          {info.promql_query}
        </pre>
      </div>
    )}

    {/* Curl Command */}
    {info.curl_command && (
      <div className="mb-4">
        <div className="flex items-center justify-between mb-2">
          <span className="text-sm font-medium text-foreground">
            Curl Command
          </span>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onCopy(info.curl_command!, `curl-${index}`)}
            className="text-xs"
          >
            {copiedKey === `curl-${index}` ? 'Copied!' : 'Copy'}
          </Button>
        </div>
        <pre className="text-xs font-mono text-foreground overflow-auto max-h-48 bg-secondary p-3 rounded border">
          {info.curl_command}
        </pre>
      </div>
    )}
  </>
)
