import { useState, useEffect } from 'react'
import { apiClient } from '@/services/apiClient'
import type { MetricsApiResponse } from '@/types/api'
import { useSessionId } from '@/stores/store'

interface ConnectMetricsFetchConfig {
  clusterId: string
  sourceType: 'msk' | 'osk'
  startDate: Date | undefined
  endDate: Date | undefined
  kind?: 'self-managed' | 'managed'
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
  clusterId,
  sourceType,
  startDate,
  endDate,
  kind,
}: ConnectMetricsFetchConfig): ConnectMetricsFetchReturn => {
  const sessionId = useSessionId()
  const [isLoading, setIsLoading] = useState(false)
  const [metricsResponse, setMetricsResponse] = useState<MetricsApiResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!clusterId) {
      setIsLoading(false)
      return
    }

    const fetchMetrics = async () => {
      setIsLoading(true)
      setError(null)

      try {
        const data = await apiClient.metrics.getConnectMetrics(
          sourceType,
          clusterId,
          sessionId,
          { startDate, endDate },
          undefined,
          kind ?? 'self-managed'
        )
        setMetricsResponse(data)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch Connect metrics')
      } finally {
        setIsLoading(false)
      }
    }

    fetchMetrics()
  }, [clusterId, sourceType, startDate, endDate, sessionId, kind])

  return {
    metricsResponse,
    isLoading,
    error,
  }
}
