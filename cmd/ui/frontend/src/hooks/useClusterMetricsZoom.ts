import { useEffect, useRef } from 'react'
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

  const { updateData, resetZoom } = zoom

  // Track previous cluster identity to detect actual cluster switches
  const prevClusterRef = useRef({ clusterName, clusterRegion })

  // Update zoom data when chart data changes
  useEffect(() => {
    updateData(chartData)
  }, [chartData, updateData])

  // Reset zoom state only when the cluster actually changes
  useEffect(() => {
    const prev = prevClusterRef.current
    if (prev.clusterName !== clusterName || prev.clusterRegion !== clusterRegion) {
      resetZoom()
      prevClusterRef.current = { clusterName, clusterRegion }
    }
  }, [clusterName, clusterRegion, resetZoom])

  return zoom
}
