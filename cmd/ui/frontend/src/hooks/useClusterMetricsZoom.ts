import { useEffect } from 'react'
import { useChartZoom } from '@/hooks/useChartZoom'
import type { ChartDataPoint } from '@/components/common/DateRangeChart'

interface ClusterMetricsZoomConfig {
  chartData: ChartDataPoint[]
  clusterName: string
  clusterRegion: string
  onDateRangeChange: (startDate: Date, endDate: Date) => void
}

/**
 * Hook to manage chart zoom with cluster-specific reset logic.
 * Automatically resets zoom when switching between clusters.
 */
export const useClusterMetricsZoom = ({
  chartData,
  clusterName,
  clusterRegion,
  onDateRangeChange,
}: ClusterMetricsZoomConfig) => {
  const zoom = useChartZoom({
    initialData: chartData,
    dataKey: 'epochTime',
    isNumericAxis: true,
    onDateRangeChange,
  })

  // Destructure the functions we need for dependency arrays
  const { updateData, resetZoom } = zoom

  // Update zoom data when chart data changes
  useEffect(() => {
    updateData(chartData)
  }, [chartData, updateData])

  // Reset zoom state when cluster changes to prevent stale domain values
  useEffect(() => {
    resetZoom()
  }, [clusterName, clusterRegion, resetZoom])

  return zoom
}
