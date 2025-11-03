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

    // Get all unique dates and sort them
    const allDates = new Set<string>()
    metrics.forEach((metric: MetricResult) => {
      if (metric && metric.start && typeof metric.start === 'string') {
        allDates.add(metric.start.split('T')[0]) // Get date part only
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
      const date =
        metric.start && typeof metric.start === 'string' ? metric.start.split('T')[0] : ''
      if (date) {
        metricsByLabel[metric.label][date] = typeof metric.value === 'number' ? metric.value : null
      }
    })

    // Create table data
    const tableData = Object.keys(metricsByLabel).map((label) => ({
      metric: label,
      values: uniqueDates.map((date) => metricsByLabel[label][date] ?? null),
    }))

    // Create CSV data
    const csvHeaders = ['Metric', ...uniqueDates]
    const csvRows = Object.keys(metricsByLabel).map((label) => [
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

      Object.keys(metricsByLabel).forEach((label) => {
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
      metrics: Object.keys(metricsByLabel).map((label) =>
        label.replace('Cluster Aggregate - ', '')
      ),
    }
  }, [metricsResponse])
}

