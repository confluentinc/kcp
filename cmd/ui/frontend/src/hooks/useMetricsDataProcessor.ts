import { useMemo } from 'react'
import { formatDateShort } from '@/lib/formatters'
import type { MetricsApiResponse, MetricResult } from '@/types/api'

interface ProcessedMetricsData {
  tableData: Array<{
    metric: string
    values: (number | null)[]
  }>
  csvData: string
  chartData: Array<{
    date: string
    formattedDate: string
    epochTime: number
    [key: string]: string | number | null | undefined
  }>
  uniqueDates: string[]
  metrics: string[]
}

/**
 * Hook to process raw metrics response into formatted data for charts, tables, and CSV
 */
export const useMetricsDataProcessor = (
  metricsResponse: MetricsApiResponse | null | undefined
): ProcessedMetricsData => {
  return useMemo(() => {
    if (!metricsResponse?.results || !Array.isArray(metricsResponse.results)) {
      return { tableData: [], csvData: '', chartData: [], uniqueDates: [], metrics: [] }
    }

    const metrics = metricsResponse.results

    // Use metadata.period to determine time granularity:
    // period >= 86400 (1 day) = daily granularity (CloudWatch), group by date
    // period < 86400 = sub-day granularity (JMX), use full timestamps
    const period = metricsResponse.metadata?.period ?? 86400
    const useFullTimestamps = period < 86400

    const getTimeKey = (start: string): string => {
      if (useFullTimestamps) return start
      return start.split('T')[0]
    }

    // Get all unique time keys and sort them
    const allDates = new Set<string>()
    metrics.forEach((metric: MetricResult) => {
      if (metric?.start && typeof metric.start === 'string') {
        allDates.add(getTimeKey(metric.start))
      }
    })
    const uniqueDates = Array.from(allDates).sort()

    // Group metrics by label
    const metricsByLabel: Record<string, Record<string, number | null>> = {}
    metrics.forEach((metric: MetricResult) => {
      if (!metric || !metric.label) return

      if (!metricsByLabel[metric.label]) {
        metricsByLabel[metric.label] = {}
      }
      const timeKey =
        metric.start && typeof metric.start === 'string' ? getTimeKey(metric.start) : ''
      if (timeKey) {
        metricsByLabel[metric.label][timeKey] = typeof metric.value === 'number' ? metric.value : null
      }
    })

    // Metric labels, sorted alphabetically so the table, dropdown, and CSV all
    // present metrics in a stable, predictable order (instead of collection order).
    const sortedLabels = Object.keys(metricsByLabel).sort((a, b) => a.localeCompare(b))

    // Create table data
    const tableData = sortedLabels.map((label) => ({
      metric: label,
      values: uniqueDates.map((date) => metricsByLabel[label][date] ?? null),
    }))

    // Create CSV data
    const csvHeaders = ['Metric', ...uniqueDates]
    const csvRows = sortedLabels.map((label) => [
      label || '',
      ...uniqueDates.map((date) => {
        const value = metricsByLabel[label][date]
        return value !== null && value !== undefined && typeof value === 'number'
          ? value.toString()
          : ''
      }),
    ])
    const csvData = [csvHeaders, ...csvRows]
      .map((row) => row.map((cell) => `"${cell || ''}"`).join(','))
      .join('\n')

    // Create chart data
    const chartData: ProcessedMetricsData['chartData'] = uniqueDates.map((date) => {
      const dateObj = new Date(date)
      const dataPoint: ProcessedMetricsData['chartData'][number] = {
        date: date,
        formattedDate: formatDateShort(date),
        epochTime: dateObj.getTime(),
      }

      sortedLabels.forEach((label) => {
        const cleanLabel = label.replace('Cluster Aggregate - ', '')
        const value = metricsByLabel[label][date]
        dataPoint[cleanLabel] = value !== null && value !== undefined ? value : null
      })

      return dataPoint
    })

    return {
      tableData,
      csvData,
      chartData,
      uniqueDates,
      metrics: sortedLabels.map((label) => label.replace('Cluster Aggregate - ', '')),
    }
  }, [metricsResponse])
}

