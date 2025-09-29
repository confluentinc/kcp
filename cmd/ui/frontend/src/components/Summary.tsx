import { useMemo, useEffect, useState } from 'react'
import { useRegions, useSummaryDateFilters } from '@/stores/appStore'
import { Download, CalendarIcon, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { format } from 'date-fns'
import { cn } from '@/lib/utils'

interface CostSummaryData {
  totalCost: number
  startDate: string | null
  endDate: string | null
  regionBreakdown: Array<{
    region: string
    cost: number
    percentage: number
  }>
}

export default function Summary() {
  const regions = useRegions()
  const { startDate, endDate, setStartDate, setEndDate, clearDates } = useSummaryDateFilters()
  const [regionCostData, setRegionCostData] = useState<Record<string, any>>({})
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [defaultsSet, setDefaultsSet] = useState(false)

  // Set default dates from metadata when data is first loaded
  useEffect(() => {
    if (defaultsSet || !regionCostData || Object.keys(regionCostData).length === 0) return

    // Extract date range from any region's metadata
    for (const [, costResponse] of Object.entries(regionCostData)) {
      if (costResponse?.metadata?.start_date && costResponse?.metadata?.end_date) {
        const metaStartDate = new Date(costResponse.metadata.start_date)
        const metaEndDate = new Date(costResponse.metadata.end_date)

        // Only set defaults if both dates are valid and no user selection has been made
        if (
          !startDate &&
          !endDate &&
          !isNaN(metaStartDate.getTime()) &&
          !isNaN(metaEndDate.getTime())
        ) {
          setStartDate(metaStartDate)
          setEndDate(metaEndDate)
          setDefaultsSet(true)
          break
        }
      }
    }
  }, [regionCostData, defaultsSet, startDate, endDate])

  // Fetch cost data for all regions
  useEffect(() => {
    if (!regions || regions.length === 0) return

    const fetchAllRegionCosts = async () => {
      setIsLoading(true)
      setError(null)

      try {
        const costPromises = regions.map(async (region) => {
          // Build URL with optional date parameters
          let url = `/costs/${encodeURIComponent(region.name)}`
          const params = new URLSearchParams()

          if (startDate) {
            params.append('startDate', startDate.toISOString())
          }
          if (endDate) {
            params.append('endDate', endDate.toISOString())
          }

          if (params.toString()) {
            url += `?${params.toString()}`
          }

          const response = await fetch(url)

          if (!response.ok) {
            throw new Error(`Failed to fetch costs for ${region.name}: ${response.status}`)
          }

          const data = await response.json()
          return { regionName: region.name, data }
        })

        const results = await Promise.all(costPromises)
        const costData: Record<string, any> = {}

        results.forEach(({ regionName, data }) => {
          costData[regionName] = data
        })

        setRegionCostData(costData)
      } catch (err) {
        console.error('Error fetching region costs:', err)
        setError(err instanceof Error ? err.message : 'Failed to fetch cost data')
      } finally {
        setIsLoading(false)
      }
    }

    fetchAllRegionCosts()
  }, [regions, startDate, endDate])

  // Process all cost data across regions
  const costSummary: CostSummaryData = useMemo(() => {
    if (!regionCostData || Object.keys(regionCostData).length === 0) {
      return {
        totalCost: 0,
        startDate: null,
        endDate: null,
        regionBreakdown: [],
      }
    }

    let totalCost = 0
    let startDate: string | null = null
    let endDate: string | null = null
    const regionCosts: Record<string, number> = {}
    const serviceCosts: Record<string, number> = {}
    const serviceRegionCount: Record<string, Set<string>> = {}

    // Process each region's cost data from API responses
    Object.entries(regionCostData).forEach(([regionName, costResponse]) => {
      if (!costResponse?.results || !Array.isArray(costResponse.results)) return

      // Extract date range from metadata if available
      if (costResponse.metadata) {
        const metaStartDate = costResponse.metadata.start_date
        const metaEndDate = costResponse.metadata.end_date

        if (metaStartDate && (!startDate || metaStartDate < startDate)) {
          startDate = metaStartDate
        }
        if (metaEndDate && (!endDate || metaEndDate > endDate)) {
          endDate = metaEndDate
        }
      }

      let regionTotal = 0

      // Process the cost results from the API - only Amazon Managed Streaming for Apache Kafka
      costResponse.results.forEach((cost: any) => {
        if (!cost || !cost.service || !cost.value) return

        // Only include Amazon Managed Streaming for Apache Kafka
        if (cost.service !== 'Amazon Managed Streaming for Apache Kafka') return

        const costValue = parseFloat(cost.value) || 0
        const service = cost.service

        regionTotal += costValue

        // Track service costs
        serviceCosts[service] = (serviceCosts[service] || 0) + costValue

        // Track which regions each service appears in
        if (!serviceRegionCount[service]) {
          serviceRegionCount[service] = new Set()
        }
        serviceRegionCount[service].add(regionName)
      })

      regionCosts[regionName] = regionTotal
      totalCost += regionTotal
    })

    // Create region breakdown with percentages
    const regionBreakdown = Object.entries(regionCosts)
      .map(([region, cost]) => ({
        region,
        cost,
        percentage: totalCost > 0 ? (cost / totalCost) * 100 : 0,
      }))
      .sort((a, b) => b.cost - a.cost)

    // Create service breakdown with percentages
    const serviceBreakdown = Object.entries(serviceCosts)
      .map(([service, cost]) => ({
        service,
        cost,
        percentage: totalCost > 0 ? (cost / totalCost) * 100 : 0,
      }))
      .sort((a, b) => b.cost - a.cost)
      .slice(0, 10) // Top 10 services

    return {
      totalCost,
      startDate,
      endDate,
      regionBreakdown,
      serviceBreakdown,
    }
  }, [regionCostData])

  const formatCurrencyDetailed = (amount: number) =>
    new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'USD',
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(amount)

  const handlePrint = () => {
    window.print()
  }

  // Show loading state
  if (isLoading) {
    return (
      <div className="p-6 space-y-8">
        <div className="text-center">
          <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100 mb-2">
            Cost Analysis Summary
          </h1>
          <p className="text-lg text-gray-600 dark:text-gray-400">
            Loading cost data for all regions...
          </p>
        </div>
        <div className="flex justify-center items-center h-64">
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500"></div>
        </div>
      </div>
    )
  }

  // Show error state
  if (error) {
    return (
      <div className="p-6 space-y-8">
        <div className="text-center">
          <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100 mb-2">
            Cost Analysis Summary
          </h1>
          <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-4 max-w-2xl mx-auto">
            <p className="text-red-800 dark:text-red-200">
              <strong>Error:</strong> {error}
            </p>
          </div>
        </div>
      </div>
    )
  }

  // Show empty state
  if (regions.length === 0) {
    return (
      <div className="p-6 space-y-8">
        <div className="text-center">
          <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100 mb-2">
            Cost Analysis Summary
          </h1>
          <p className="text-lg text-gray-600 dark:text-gray-400">
            Upload a KCP state file to view cost analysis
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-8 print:block">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100">Summary</h1>
        </div>
        <div className="flex items-center gap-4">
          <Button
            onClick={handlePrint}
            variant="outline"
            size="sm"
          >
            <Download className="h-4 w-4 mr-2" />
            Export PDF
          </Button>
        </div>
      </div>

      {/* Date Picker Controls */}
      <div className="bg-white dark:bg-gray-800 rounded-xl p-6 shadow-lg border border-gray-200 dark:border-gray-700">
        <div className="flex flex-col sm:flex-row gap-4 mb-6">
          <div className="flex flex-col space-y-2">
            <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Start Date
            </label>
            <div className="relative">
              <Popover>
                <PopoverTrigger asChild>
                  <Button
                    variant="outline"
                    className={cn(
                      'w-[240px] justify-start text-left font-normal pr-10',
                      !startDate && 'text-muted-foreground'
                    )}
                  >
                    <CalendarIcon className="mr-2 h-4 w-4" />
                    {startDate ? format(startDate, 'PPP') : 'Pick a start date'}
                  </Button>
                </PopoverTrigger>
                <PopoverContent
                  className="w-auto p-0"
                  align="start"
                >
                  <Calendar
                    mode="single"
                    selected={startDate}
                    onSelect={setStartDate}
                  />
                </PopoverContent>
              </Popover>
              {startDate && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="absolute right-2 top-1/2 -translate-y-1/2 h-7 w-7 p-0 z-10 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700 shadow-sm"
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    setStartDate(undefined)
                  }}
                  title="Clear start date"
                >
                  <X className="h-3 w-3 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" />
                </Button>
              )}
            </div>
          </div>

          <div className="flex flex-col space-y-2">
            <label className="text-sm font-medium text-gray-700 dark:text-gray-300">End Date</label>
            <div className="relative">
              <Popover>
                <PopoverTrigger asChild>
                  <Button
                    variant="outline"
                    className={cn(
                      'w-[240px] justify-start text-left font-normal pr-10',
                      !endDate && 'text-muted-foreground'
                    )}
                  >
                    <CalendarIcon className="mr-2 h-4 w-4" />
                    {endDate ? format(endDate, 'PPP') : 'Pick an end date'}
                  </Button>
                </PopoverTrigger>
                <PopoverContent
                  className="w-auto p-0"
                  align="start"
                >
                  <Calendar
                    mode="single"
                    selected={endDate}
                    onSelect={setEndDate}
                  />
                </PopoverContent>
              </Popover>
              {endDate && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="absolute right-2 top-1/2 -translate-y-1/2 h-7 w-7 p-0 z-10 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700 shadow-sm"
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    setEndDate(undefined)
                  }}
                  title="Clear end date"
                >
                  <X className="h-3 w-3 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" />
                </Button>
              )}
            </div>
          </div>

          <div className="flex flex-col justify-end">
            <Button
              variant="outline"
              onClick={clearDates}
              className="w-full sm:w-auto"
            >
              Clear All
            </Button>
          </div>
        </div>
      </div>

      {/* Regional Breakdown Table */}
      <div className="w-full">
        <div className="bg-white dark:bg-gray-800 rounded-xl p-6 shadow-lg border border-gray-200 dark:border-gray-700">
          <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-6">
            MSK Cost by Region
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-gray-200 dark:border-gray-700">
                  <th className="text-left py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                    Region
                  </th>
                  <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                    Cost
                  </th>
                </tr>
              </thead>
              <tbody>
                {costSummary.regionBreakdown.map((region) => (
                  <tr
                    key={region.region}
                    className="border-b border-gray-100 dark:border-gray-700/50"
                  >
                    <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 font-medium">
                      {region.region}
                    </td>
                    <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                      {formatCurrencyDetailed(region.cost)}
                    </td>
                  </tr>
                ))}
                {/* Total Row */}
                <tr className="border-t-2 border-gray-300 dark:border-gray-600 bg-gray-50 dark:bg-gray-700/50">
                  <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100">
                    Total
                  </td>
                  <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(costSummary.totalCost)}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </div>
  )
}
