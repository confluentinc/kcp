import { useState, useEffect } from 'react'
import { apiClient } from '@/services/apiClient'
import type { MetricsApiResponse } from '@/types/api'
import { useSessionId } from '@/stores/store'

interface ConnectMetricsFetchConfig {
  isActive: boolean
  clusterId: string
  startDate: Date | undefined
  endDate: Date | undefined
}

interface ConnectMetricsFetchReturn {
  metricsResponse: MetricsApiResponse | null
  isLoading: boolean
  error: string | null
}

/**
 * Hook to fetch Connect metrics data from the API.
 * Automatically refetches when dates change or cluster changes.
 */
export const useConnectMetricsFetch = ({
  isActive,
  clusterId,
  startDate,
  endDate,
}: ConnectMetricsFetchConfig): ConnectMetricsFetchReturn => {
  const sessionId = useSessionId()
  const [isLoading, setIsLoading] = useState(false)
  const [metricsResponse, setMetricsResponse] = useState<MetricsApiResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!isActive || !clusterId) {
      setIsLoading(false)
      return
    }

    const fetchMetrics = async () => {
      setIsLoading(true)
      setError(null)

      try {
        const data = await apiClient.metrics.getOSKConnectMetrics(clusterId, sessionId, {
          startDate,
          endDate,
        })
        setMetricsResponse(data)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch Connect metrics')
      } finally {
        setIsLoading(false)
      }
    }

    fetchMetrics()
  }, [isActive, clusterId, startDate, endDate, sessionId])

  return {
    metricsResponse,
    isLoading,
    error,
  }
}
