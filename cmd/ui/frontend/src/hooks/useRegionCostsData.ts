import { useMemo } from 'react'
import { formatDateShort } from '@/lib/formatters'
import { formatCostTypeLabel } from '@/lib/costTypeUtils'
import type { CostsApiResponse, CostResult } from '@/types/api'

interface ProcessedData {
  tableData: Array<{
    service: string
    usageType: string
    values: number[]
    total: number
  }>
  filteredTableData: Array<{
    service: string
    usageType: string
    values: number[]
    total: number
  }>
  csvData: string
  chartData: Array<{
    date: string
    formattedDate: string
    epochTime: number
    [key: string]: string | number
  }>
  chartOptions: Array<{
    value: string
    label: string
    type: 'service'
  }>
  getUsageTypesForService: (serviceName: string) => string[]
  uniqueDates: string[]
  services: string[]
  serviceTotals: Record<string, number>
}

export function useRegionCostsData(
  costsResponse: CostsApiResponse | null | undefined,
  selectedTableService: string,
  selectedCostType: string
): ProcessedData {
  return useMemo(() => {
    if (!costsResponse?.results || !Array.isArray(costsResponse.results)) {
      return {
        tableData: [],
        filteredTableData: [],
        csvData: '',
        chartData: [],
        chartOptions: [],
        getUsageTypesForService: () => [],
        uniqueDates: [],
        services: [],
        serviceTotals: {},
      }
    }

    const costs = costsResponse.results
    const aggregates = costsResponse.aggregates || {}

    // Get all unique dates and services from the raw data
    const allDates = new Set<string>()
    const allServices = new Set<string>()
    costs.forEach((cost: CostResult) => {
      if (cost && cost.start && typeof cost.start === 'string') {
        allDates.add(cost.start) // Use full date string
      }
      if (cost && cost.service) {
        allServices.add(cost.service)
      }
    })

    const uniqueDates = Array.from(allDates).sort()
    const services = Array.from(allServices).sort()

    // Extract service totals from backend aggregates
    const serviceTotals: Record<string, number> = {}
    const usageTypeTotals: Record<string, number> = {}

    // Use backend aggregates (nested structure: service -> cost_type -> usage_type -> {sum, avg, max, min})
    // Filter out usage_quantity cost type
    services.forEach((service) => {
      if (aggregates[service]) {
        const serviceAggregates = aggregates[service]

        // Skip usage_quantity cost type
        if (selectedCostType === 'usage_quantity') return

        // Get service total directly from the selected cost type
        if (serviceAggregates[selectedCostType]?.total !== undefined) {
          serviceTotals[service] = serviceAggregates[selectedCostType].total
        }

        // Extract usage type totals for the selected cost type
        if (serviceAggregates[selectedCostType]) {
          const costTypeAggregates = serviceAggregates[selectedCostType]
          Object.keys(costTypeAggregates).forEach((usageType) => {
            if (usageType === 'total') return // Skip the service total

            const usageTypeAggregate = costTypeAggregates[usageType]
            // Check if it's a UsageTypeAggregate object with sum property
            if (
              typeof usageTypeAggregate === 'object' &&
              usageTypeAggregate !== null &&
              'sum' in usageTypeAggregate &&
              usageTypeAggregate.sum !== undefined
            ) {
              const usageKey = `${service}:${usageType}`
              usageTypeTotals[usageKey] = usageTypeAggregate.sum
            }
          })
        }
      }
    })

    // Group costs by service, usage type, and date for chart data
    // Filter out usage_quantity cost type
    const costsByServiceAndUsage: Record<string, Record<string, Record<string, number>>> = {}
    costs.forEach((cost: CostResult) => {
      if (!cost || !cost.service || !cost.usage_type || !cost.start || !cost.values) return

      // Skip usage_quantity cost type
      if (selectedCostType === 'usage_quantity') return

      const service = cost.service
      const usageType = cost.usage_type
      const date = cost.start
      const costValue = cost.values[selectedCostType]
      const value = costValue !== undefined ? parseFloat(String(costValue)) || 0 : 0

      // Initialize nested structure
      if (!costsByServiceAndUsage[service]) {
        costsByServiceAndUsage[service] = {}
      }
      if (!costsByServiceAndUsage[service][usageType]) {
        costsByServiceAndUsage[service][usageType] = {}
      }
      if (!costsByServiceAndUsage[service][usageType][date]) {
        costsByServiceAndUsage[service][usageType][date] = 0
      }

      costsByServiceAndUsage[service][usageType][date] += value
    })

    // Create table data using backend aggregates for totals (with fallback)
    const tableData: Array<{
      service: string
      usageType: string
      values: number[]
      total: number
    }> = []
    services.forEach((service) => {
      if (costsByServiceAndUsage[service]) {
        Object.keys(costsByServiceAndUsage[service]).forEach((usageType) => {
          const usageKey = `${service}:${usageType}`
          // Use backend aggregates (from nested structure) - NO frontend calculation
          const total = usageTypeTotals[usageKey] || 0

          tableData.push({
            service,
            usageType,
            values: uniqueDates.map(
              (date) => costsByServiceAndUsage[service][usageType][date] || 0
            ),
            total: total, // ✅ From backend aggregates, not calculated here
          })
        })
      }
    })

    // Sort table data by service, then by usage type
    tableData.sort((a, b) => {
      if (a.service !== b.service) {
        return a.service.localeCompare(b.service)
      }
      return a.usageType.localeCompare(b.usageType)
    })

    // Filter table data by selected service
    const filteredTableData = selectedTableService
      ? tableData.filter((row) => row.service === selectedTableService)
      : tableData

    // Create CSV data
    const csvHeaders = [
      'Service',
      'Usage Type',
      `Total (${formatCostTypeLabel(selectedCostType)})`,
      ...uniqueDates,
    ]
    const csvRows = tableData.map((row) => [
      row.service,
      row.usageType,
      row.total.toFixed(2),
      ...row.values.map((value: number) => value.toFixed(2)),
    ])
    const csvData = [csvHeaders, ...csvRows]
      .map((row) => row.map((cell) => `"${cell || ''}"`).join(','))
      .join('\n')

    // Create chart data (dates with both service totals and individual usage types)
    const chartData: ProcessedData['chartData'] = uniqueDates.map((date) => {
      const dateObj = new Date(date)
      const dataPoint: ProcessedData['chartData'][number] = {
        date: date,
        formattedDate: formatDateShort(date),
        epochTime: dateObj.getTime(),
      }

      // Add service-level aggregates
      services.forEach((service) => {
        let serviceCostForDate = 0
        if (costsByServiceAndUsage[service]) {
          Object.keys(costsByServiceAndUsage[service]).forEach((usageType) => {
            serviceCostForDate += costsByServiceAndUsage[service][usageType][date] || 0
          })
        }
        dataPoint[service] = serviceCostForDate
      })

      // Add individual usage types
      services.forEach((service) => {
        if (costsByServiceAndUsage[service]) {
          Object.keys(costsByServiceAndUsage[service]).forEach((usageType) => {
            const usageKey = `${service}:${usageType}`
            dataPoint[usageKey] = costsByServiceAndUsage[service][usageType][date] || 0
          })
        }
      })

      return dataPoint
    })

    // Create chart options (services only)
    const chartOptions = services.map((service) => ({
      value: service,
      label: service,
      type: 'service' as const,
    }))

    // Get usage types for the selected service
    const getUsageTypesForService = (serviceName: string) => {
      if (!costsByServiceAndUsage[serviceName]) return []
      return Object.keys(costsByServiceAndUsage[serviceName]).sort()
    }

    return {
      tableData,
      filteredTableData,
      csvData,
      chartData,
      chartOptions,
      getUsageTypesForService,
      uniqueDates,
      services,
      serviceTotals,
    }
  }, [costsResponse, selectedTableService, selectedCostType])
}

