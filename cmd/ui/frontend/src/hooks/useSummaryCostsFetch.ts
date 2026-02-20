import { useEffect, useState } from 'react'
import { apiClient } from '@/services/apiClient'
import type { CostsApiResponse } from '@/types/api'
import type { Region } from '@/types'
import { useSessionId } from '@/stores/store'

interface UseSummaryCostsFetchConfig {
  regions: Region[]
  startDate: Date | undefined
  endDate: Date | undefined
}

interface UseSummaryCostsFetchReturn {
  regionCostData: Record<string, CostsApiResponse>
  isLoading: boolean
  error: string | null
}

/**
 * Hook to fetch cost data for multiple regions in parallel.
 * Handles loading states, error handling, and aggregates results.
 *
 * @param {UseSummaryCostsFetchConfig} config - Configuration object
 * @returns {UseSummaryCostsFetchReturn} Cost data, loading state, and error
 */
export const useSummaryCostsFetch = ({
  regions,
  startDate,
  endDate,
}: UseSummaryCostsFetchConfig): UseSummaryCostsFetchReturn => {
  const sessionId = useSessionId()
  const [regionCostData, setRegionCostData] = useState<Record<string, CostsApiResponse>>({})
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!regions || regions.length === 0) {
      setRegionCostData({})
      setIsLoading(false)
      return
    }

    const fetchAllRegionCosts = async () => {
      setIsLoading(true)
      setError(null)

      try {
        const costPromises = regions.map(async (region) => {
          const data = await apiClient.costs.getCosts(region.name, sessionId, {
            startDate,
            endDate,
          })
          return { regionName: region.name, data }
        })

        const results = await Promise.all(costPromises)
        const costData: Record<string, CostsApiResponse> = {}

        results.forEach(({ regionName, data }) => {
          costData[regionName] = data
        })

        setRegionCostData(costData)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch cost data')
        setRegionCostData({})
      } finally {
        setIsLoading(false)
      }
    }

    fetchAllRegionCosts()
  }, [regions, startDate, endDate, sessionId])

  return {
    regionCostData,
    isLoading,
    error,
  }
}

