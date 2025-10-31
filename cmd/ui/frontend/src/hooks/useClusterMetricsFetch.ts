import { useState, useEffect } from 'react'
import { apiClient } from '@/services/apiClient'
import type { MetricsApiResponse } from '@/types/api'

interface ClusterMetricsFetchConfig {
  isActive: boolean
  clusterName: string
  clusterRegion: string
  startDate: Date | undefined
  endDate: Date | undefined
}

interface ClusterMetricsFetchReturn {
  metricsResponse: MetricsApiResponse | null
  isLoading: boolean
  error: string | null
}

/**
 * Hook to fetch cluster metrics data from the API.
 * Automatically refetches when dates change or cluster changes.
 */
export function useClusterMetricsFetch({
  isActive,
  clusterName,
  clusterRegion,
  startDate,
  endDate,
}: ClusterMetricsFetchConfig): ClusterMetricsFetchReturn {
  const [isLoading, setIsLoading] = useState(false)
  const [metricsResponse, setMetricsResponse] = useState<MetricsApiResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  // Fetch metrics when tab becomes active or dates/cluster change
  useEffect(() => {
    if (!isActive || !clusterName) {
      setIsLoading(false)
      return
    }

    const fetchMetrics = async () => {
      setIsLoading(true)
      setError(null)

      try {
        const region = clusterRegion || 'unknown'

        const data = await apiClient.metrics.getMetrics(region, clusterName, {
          startDate,
          endDate,
        })

        setMetricsResponse(data)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch metrics')
      } finally {
        setIsLoading(false)
      }
    }

    fetchMetrics()
  }, [isActive, clusterName, clusterRegion, startDate, endDate])

  return {
    metricsResponse,
    isLoading,
    error,
  }
}
