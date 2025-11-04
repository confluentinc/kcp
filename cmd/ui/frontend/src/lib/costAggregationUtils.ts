import type { CostsApiResponse } from '@/types/api'
import { COST_TYPES, AWS_SERVICES } from '@/constants'
import { formatDateShort } from './formatters'

export interface RegionBreakdown {
  region: string
  unblended_cost: number
  blended_cost: number
  amortized_cost: number
  net_amortized_cost: number
  net_unblended_cost: number
}

export interface ChartDataPoint {
  date: string
  formattedDate?: string
  epochTime: number
  [regionName: string]: string | number | null | undefined
}

export interface CostSummaryData {
  startDate: string | null
  endDate: string | null
  regionBreakdown: RegionBreakdown[]
  chartData: ChartDataPoint[]
}

/**
 * Extracts the overall date range from multiple region cost responses.
 * Returns the minimum start date and maximum end date across all regions.
 */
export const extractMetadataDateRange = (
  regionCostData: Record<string, CostsApiResponse>
): { startDate: string | null; endDate: string | null } => {
  let startDate: string | null = null
  let endDate: string | null = null

  Object.values(regionCostData).forEach((costResponse) => {
    if (!costResponse?.metadata) return

    const metaStartDate = costResponse.metadata.start_date
    const metaEndDate = costResponse.metadata.end_date

    if (metaStartDate && (!startDate || metaStartDate < startDate)) {
      startDate = metaStartDate
    }
    if (metaEndDate && (!endDate || metaEndDate > endDate)) {
      endDate = metaEndDate
    }
  })

  return { startDate, endDate }
}

/**
 * Aggregates region costs from API responses using the aggregates structure.
 * Processes only Amazon Managed Streaming for Apache Kafka service.
 *
 * @param {Record<string, CostsApiResponse>} regionCostData - Cost data by region name
 * @returns {Record<string, Record<string, number>>} Aggregated costs by region and cost type
 */
export const aggregateRegionCosts = (
  regionCostData: Record<string, CostsApiResponse>
): Record<string, Record<string, number>> => {
  const regionCosts: Record<string, Record<string, number>> = {}

  const costTypes = [
    COST_TYPES.UNBLENDED_COST,
    COST_TYPES.BLENDED_COST,
    COST_TYPES.AMORTIZED_COST,
    COST_TYPES.NET_AMORTIZED_COST,
    COST_TYPES.NET_UNBLENDED_COST,
  ]

  Object.entries(regionCostData).forEach(([regionName, costResponse]) => {
    if (!costResponse?.aggregates) return

    // Initialize region costs for all cost types
    regionCosts[regionName] = {}
    costTypes.forEach((costType) => {
      regionCosts[regionName][costType] = 0
    })

    const aggregates = costResponse.aggregates

    // Process aggregates: service -> cost_type -> usage_type -> {sum, avg, max, min}
    // Only include Amazon Managed Streaming for Apache Kafka
    Object.entries(aggregates).forEach(([service, serviceAggregates]) => {
      if (service !== AWS_SERVICES.MSK) return

      // Process each cost type
      costTypes.forEach((costType) => {
        if (serviceAggregates[costType]?.total !== undefined) {
          regionCosts[regionName][costType] += serviceAggregates[costType].total
        }
      })
    })
  })

  return regionCosts
}

/**
 * Processes daily cost data from raw API results into chart-ready format.
 * Filters for MSK service only and aggregates by date and region.
 *
 * @param {Record<string, CostsApiResponse>} regionCostData - Cost data by region name
 * @returns {Record<string, Record<string, Record<string, number>>>} Daily costs by date, region, and cost type
 */
export const processDailyCosts = (
  regionCostData: Record<string, CostsApiResponse>
): Record<string, Record<string, Record<string, number>>> => {
  const dailyRegionCosts: Record<string, Record<string, Record<string, number>>> = {}

  const costTypes = [
    COST_TYPES.UNBLENDED_COST,
    COST_TYPES.BLENDED_COST,
    COST_TYPES.AMORTIZED_COST,
    COST_TYPES.NET_AMORTIZED_COST,
    COST_TYPES.NET_UNBLENDED_COST,
  ]

  Object.entries(regionCostData).forEach(([regionName, costResponse]) => {
    if (!costResponse?.results || !Array.isArray(costResponse.results)) return

    costResponse.results.forEach((cost) => {
      if (!cost || !cost.start || !cost.service || !cost.values) return

      // Only include Amazon Managed Streaming for Apache Kafka
      if (cost.service !== AWS_SERVICES.MSK) return

      const date = cost.start

      if (!dailyRegionCosts[date]) {
        dailyRegionCosts[date] = {}
      }
      if (!dailyRegionCosts[date][regionName]) {
        dailyRegionCosts[date][regionName] = {}
        costTypes.forEach((costType) => {
          dailyRegionCosts[date][regionName][costType] = 0
        })
      }

      // Add costs for each cost type
      costTypes.forEach((costType) => {
        const costValue = cost.values[costType]
        const value =
          typeof costValue === 'string'
            ? parseFloat(costValue)
            : typeof costValue === 'number'
            ? costValue
            : 0
        dailyRegionCosts[date][regionName][costType] += value || 0
      })
    })
  })

  return dailyRegionCosts
}

/**
 * Creates chart data points from aggregated region costs and daily costs.
 * Formats dates and includes epoch time for chart rendering.
 *
 * @param {Record<string, Record<string, number>>} regionCosts - Aggregated costs by region and cost type
 * @param {Record<string, Record<string, Record<string, number>>>} dailyRegionCosts - Daily costs by date, region, and cost type
 * @param {string} selectedCostType - The cost type to display in the chart
 * @returns {ChartDataPoint[]} Array of chart data points
 */
export const createCostChartData = (
  regionCosts: Record<string, Record<string, number>>,
  dailyRegionCosts: Record<string, Record<string, Record<string, number>>>,
  selectedCostType: string
): ChartDataPoint[] => {
  const allDates = new Set<string>()
  Object.keys(dailyRegionCosts).forEach((date) => allDates.add(date))

  const sortedDates = Array.from(allDates).sort()

  return sortedDates.map((date) => {
    const dateObj = new Date(date)
    const dataPoint: ChartDataPoint = {
      date: date,
      formattedDate: formatDateShort(date),
      epochTime: dateObj.getTime(),
    }

    // Add each region's cost for the selected cost type
    Object.keys(regionCosts).forEach((regionName) => {
      const regionDailyCosts = dailyRegionCosts[date]?.[regionName]
      dataPoint[regionName] = regionDailyCosts?.[selectedCostType] || 0
    })

    return dataPoint
  })
}

/**
 * Creates a complete cost summary from region cost data.
 * Aggregates costs, processes daily data, and creates chart-ready format.
 *
 * @param {Record<string, CostsApiResponse>} regionCostData - Cost data by region name
 * @param {string} selectedChartCostType - The cost type to display in the chart
 * @returns {CostSummaryData} Complete cost summary with breakdown and chart data
 */
export const createCostSummary = (
  regionCostData: Record<string, CostsApiResponse>,
  selectedChartCostType: string
): CostSummaryData => {
  if (!regionCostData || Object.keys(regionCostData).length === 0) {
    return {
      startDate: null,
      endDate: null,
      regionBreakdown: [],
      chartData: [],
    }
  }

  // Extract overall date range
  const { startDate, endDate } = extractMetadataDateRange(regionCostData)

  // Aggregate region costs
  const regionCosts = aggregateRegionCosts(regionCostData)

  // Create region breakdown with all cost types
  const regionBreakdown: RegionBreakdown[] = Object.entries(regionCosts)
    .map(([region, costs]) => ({
      region,
      unblended_cost: costs.unblended_cost || 0,
      blended_cost: costs.blended_cost || 0,
      amortized_cost: costs.amortized_cost || 0,
      net_amortized_cost: costs.net_amortized_cost || 0,
      net_unblended_cost: costs.net_unblended_cost || 0,
    }))
    .sort((a, b) => b.unblended_cost - a.unblended_cost) // Sort by unblended cost

  // Process daily costs
  const dailyRegionCosts = processDailyCosts(regionCostData)

  // Create chart data
  const chartData = createCostChartData(regionCosts, dailyRegionCosts, selectedChartCostType)

  return {
    startDate,
    endDate,
    regionBreakdown,
    chartData,
  }
}
