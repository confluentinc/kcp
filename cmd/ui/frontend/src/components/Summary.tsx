import { useMemo, useEffect, useState } from 'react'
import { useRegions, useSummaryDateFilters } from '@/stores/appStore'
import { Download, CalendarIcon, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
  ReferenceArea,
} from 'recharts'
import { format } from 'date-fns'
import { cn } from '@/lib/utils'
import { useChartZoom } from '@/lib/useChartZoom'

interface CostSummaryData {
  startDate: string | null
  endDate: string | null
  regionBreakdown: Array<{
    region: string
    unblended_cost: number
    blended_cost: number
    amortized_cost: number
    net_amortized_cost: number
    net_unblended_cost: number
  }>
  chartData: Array<{
    date: string
    formattedDate: string
    [regionName: string]: string | number
  }>
}

export default function Summary() {
  const regions = useRegions()
  const { startDate, endDate, setStartDate, setEndDate } = useSummaryDateFilters()
  const [regionCostData, setRegionCostData] = useState<Record<string, any>>({})
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [defaultsSet, setDefaultsSet] = useState(false)
  const [selectedChartCostType, setSelectedChartCostType] = useState<string>('unblended_cost')

  // Get metadata dates from any region's data
  const getMetadataDates = () => {
    for (const [, costResponse] of Object.entries(regionCostData)) {
      if (costResponse?.metadata?.start_date && costResponse?.metadata?.end_date) {
        return {
          startDate: costResponse.metadata.start_date,
          endDate: costResponse.metadata.end_date,
        }
      }
    }
    return null
  }

  // Set default dates from metadata when data is first loaded
  useEffect(() => {
    if (defaultsSet || !regionCostData || Object.keys(regionCostData).length === 0) return

    // Get metadata dates from any region's data (inline to avoid dependency issues)
    let metadataDates = null
    for (const [, costResponse] of Object.entries(regionCostData)) {
      if (costResponse?.metadata?.start_date && costResponse?.metadata?.end_date) {
        metadataDates = {
          startDate: costResponse.metadata.start_date,
          endDate: costResponse.metadata.end_date,
        }
        break
      }
    }

    if (metadataDates) {
      const metaStartDate = new Date(metadataDates.startDate)
      const metaEndDate = new Date(metadataDates.endDate)

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
      }
    }
  }, [regionCostData, defaultsSet, startDate, endDate, setStartDate, setEndDate])

  // Custom reset functions that use metadata dates
  const resetToMetadataDates = () => {
    const metadataDates = getMetadataDates()
    if (metadataDates) {
      setStartDate(new Date(metadataDates.startDate))
      setEndDate(new Date(metadataDates.endDate))
      resetZoom() // Reset chart zoom when dates are reset
    }
  }

  const resetStartDateToMetadata = () => {
    const metadataDates = getMetadataDates()
    if (metadataDates) {
      setStartDate(new Date(metadataDates.startDate))
      resetZoom() // Reset chart zoom when start date is reset
    }
  }

  const resetEndDateToMetadata = () => {
    const metadataDates = getMetadataDates()
    if (metadataDates) {
      setEndDate(new Date(metadataDates.endDate))
      resetZoom() // Reset chart zoom when end date is reset
    }
  }

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
        startDate: null,
        endDate: null,
        regionBreakdown: [],
        chartData: [],
      }
    }

    let startDate: string | null = null
    let endDate: string | null = null
    const regionCosts: Record<string, Record<string, number>> = {}

    // Define the cost types we want to include
    const costTypes = [
      'unblended_cost',
      'blended_cost',
      'amortized_cost',
      'net_amortized_cost',
      'net_unblended_cost',
    ]

    // Process each region's cost data from API responses using aggregates
    Object.entries(regionCostData).forEach(([regionName, costResponse]) => {
      if (!costResponse?.aggregates) return

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

      // Initialize region costs for all cost types
      regionCosts[regionName] = {}
      costTypes.forEach((costType) => {
        regionCosts[regionName][costType] = 0
      })

      const aggregates = costResponse.aggregates

      // Process aggregates using the new structure: service -> cost_type -> usage_type -> {sum, avg, max, min}
      // Only include Amazon Managed Streaming for Apache Kafka
      Object.entries(aggregates).forEach(([service, serviceAggregates]: [string, any]) => {
        // Only include Amazon Managed Streaming for Apache Kafka
        if (service !== 'Amazon Managed Streaming for Apache Kafka') return

        // Process each cost type
        costTypes.forEach((costType) => {
          if (serviceAggregates[costType]?.total !== undefined) {
            regionCosts[regionName][costType] += serviceAggregates[costType].total
          }
        })
      })
    })

    // Create region breakdown with all cost types
    const regionBreakdown = Object.entries(regionCosts)
      .map(([region, costs]) => ({
        region,
        unblended_cost: costs.unblended_cost || 0,
        blended_cost: costs.blended_cost || 0,
        amortized_cost: costs.amortized_cost || 0,
        net_amortized_cost: costs.net_amortized_cost || 0,
        net_unblended_cost: costs.net_unblended_cost || 0,
      }))
      .sort((a, b) => b.unblended_cost - a.unblended_cost) // Sort by unblended cost

    // Create chart data by processing daily costs for each region
    const dailyRegionCosts: Record<string, Record<string, Record<string, number>>> = {}
    const allDates = new Set<string>()

    // Process raw cost data to get daily costs by region
    Object.entries(regionCostData).forEach(([regionName, costResponse]) => {
      if (!costResponse?.results || !Array.isArray(costResponse.results)) return

      costResponse.results.forEach((cost: any) => {
        if (!cost || !cost.start || !cost.service || !cost.values) return

        // Only include Amazon Managed Streaming for Apache Kafka
        if (cost.service !== 'Amazon Managed Streaming for Apache Kafka') return

        const date = cost.start
        allDates.add(date)

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
          const value = parseFloat(cost.values[costType]) || 0
          dailyRegionCosts[date][regionName][costType] += value
        })
      })
    })

    // Create chart data
    const sortedDates = Array.from(allDates).sort()
    const chartData = sortedDates.map((date) => {
      const dateObj = new Date(date)
      const dataPoint: any = {
        date: date,
        formattedDate: dateObj.toLocaleDateString('en-US', {
          month: 'short',
          day: 'numeric',
        }),
        epochTime: dateObj.getTime(),
      }

      // Add each region's cost for the selected cost type
      Object.keys(regionCosts).forEach((regionName) => {
        const regionDailyCosts = dailyRegionCosts[date]?.[regionName]
        dataPoint[regionName] = regionDailyCosts?.[selectedChartCostType] || 0
      })

      return dataPoint
    })

    return {
      startDate,
      endDate,
      regionBreakdown,
      chartData,
    }
  }, [regionCostData, selectedChartCostType])

  // Initialize zoom functionality
  const {
    data: zoomData,
    left,
    right,
    refAreaLeft,
    refAreaRight,
    handleMouseDown,
    handleMouseMove,
    zoom,
    resetZoom,
    updateData,
  } = useChartZoom({
    initialData: costSummary.chartData,
    dataKey: 'epochTime',
    isNumericAxis: true,
    onDateRangeChange: (startDate, endDate) => {
      setStartDate(startDate)
      setEndDate(endDate)
    },
  })

  // Update zoom data when costSummary changes
  useEffect(() => {
    updateData(costSummary.chartData)
  }, [costSummary.chartData, updateData])

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
          <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100">MSK Cost Summary</h1>
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
                    resetStartDateToMetadata()
                  }}
                  title="Reset to metadata start date"
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
                    resetEndDateToMetadata()
                  }}
                  title="Reset to metadata end date"
                >
                  <X className="h-3 w-3 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" />
                </Button>
              )}
            </div>
          </div>

          <div className="flex flex-col justify-end">
            <Button
              variant="outline"
              onClick={resetToMetadataDates}
              className="w-full sm:w-auto"
            >
              Reset
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
                    Unblended Cost
                  </th>
                  <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                    Blended Cost
                  </th>
                  <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                    Amortized Cost
                  </th>
                  <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                    Net Amortized Cost
                  </th>
                  <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                    Net Unblended Cost
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
                      {formatCurrencyDetailed(region.unblended_cost)}
                    </td>
                    <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                      {formatCurrencyDetailed(region.blended_cost)}
                    </td>
                    <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                      {formatCurrencyDetailed(region.amortized_cost)}
                    </td>
                    <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                      {formatCurrencyDetailed(region.net_amortized_cost)}
                    </td>
                    <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                      {formatCurrencyDetailed(region.net_unblended_cost)}
                    </td>
                  </tr>
                ))}
                {/* Total Row */}
                <tr className="border-t-2 border-gray-300 dark:border-gray-600 bg-gray-50 dark:bg-gray-700/50">
                  <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100">
                    Total
                  </td>
                  <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(
                      costSummary.regionBreakdown.reduce(
                        (sum, region) => sum + region.unblended_cost,
                        0
                      )
                    )}
                  </td>
                  <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(
                      costSummary.regionBreakdown.reduce(
                        (sum, region) => sum + region.blended_cost,
                        0
                      )
                    )}
                  </td>
                  <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(
                      costSummary.regionBreakdown.reduce(
                        (sum, region) => sum + region.amortized_cost,
                        0
                      )
                    )}
                  </td>
                  <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(
                      costSummary.regionBreakdown.reduce(
                        (sum, region) => sum + region.net_amortized_cost,
                        0
                      )
                    )}
                  </td>
                  <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(
                      costSummary.regionBreakdown.reduce(
                        (sum, region) => sum + region.net_unblended_cost,
                        0
                      )
                    )}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {/* Cost Over Time Chart */}
      <div className="w-full">
        <div className="bg-white dark:bg-gray-800 rounded-xl p-6 shadow-lg border border-gray-200 dark:border-gray-700">
          <div className="flex items-center justify-between mb-6">
            <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
              MSK Cost Over Time by Region
            </h3>
            <div className="flex items-center gap-4">
              <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
                Cost Type:
              </label>
              <Select
                value={selectedChartCostType}
                onValueChange={setSelectedChartCostType}
              >
                <SelectTrigger className="w-[200px]">
                  <SelectValue placeholder="Select cost type" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="unblended_cost">Unblended Cost</SelectItem>
                  <SelectItem value="blended_cost">Blended Cost</SelectItem>
                  <SelectItem value="amortized_cost">Amortized Cost</SelectItem>
                  <SelectItem value="net_amortized_cost">Net Amortized Cost</SelectItem>
                  <SelectItem value="net_unblended_cost">Net Unblended Cost</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {costSummary.chartData.length > 0 ? (
            <div
              className="h-96"
              style={{ userSelect: 'none' }}
            >
              <ResponsiveContainer
                width="100%"
                height="100%"
              >
                <AreaChart
                  data={zoomData}
                  margin={{ top: 5, right: 30, left: 20, bottom: 5 }}
                  onMouseDown={handleMouseDown}
                  onMouseMove={handleMouseMove}
                  onMouseUp={zoom}
                >
                  <CartesianGrid
                    strokeDasharray="3 3"
                    className="opacity-30"
                  />
                  <XAxis
                    allowDataOverflow
                    dataKey="epochTime"
                    domain={[left, right]}
                    type="number"
                    scale="time"
                    tickFormatter={(value) =>
                      new Date(value).toLocaleDateString('en-US', {
                        month: 'short',
                        day: 'numeric',
                      })
                    }
                    tick={{ fontSize: 12, fill: 'currentColor' }}
                    className="text-gray-700 dark:text-gray-200"
                  />
                  <YAxis
                    tick={{ fontSize: 12, fill: 'currentColor' }}
                    className="text-gray-700 dark:text-gray-200"
                    tickFormatter={(value) => `$${value.toFixed(2)}`}
                  />
                  <Tooltip
                    content={({ active, payload, label }) => {
                      if (active && payload && payload.length > 0) {
                        const nonZeroEntries = payload.filter(
                          (entry) => entry.value && entry.value > 0
                        )

                        if (nonZeroEntries.length === 0) return null

                        const sortedEntries = nonZeroEntries.sort(
                          (a, b) => (b.value || 0) - (a.value || 0)
                        )

                        return (
                          <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-lg p-3 shadow-lg">
                            <p className="text-gray-700 dark:text-gray-200 text-sm font-medium mb-2">
                              {label}
                            </p>
                            <div className="space-y-1">
                              {sortedEntries.map((entry, index) => (
                                <p
                                  key={index}
                                  className="text-gray-900 dark:text-gray-100 text-sm flex items-center justify-between"
                                >
                                  <span
                                    className="flex items-center"
                                    style={{ color: entry.color }}
                                  >
                                    <span
                                      className="inline-block w-3 h-3 rounded-full mr-2"
                                      style={{ backgroundColor: entry.color }}
                                    ></span>
                                    {entry.name}:
                                  </span>
                                  <span className="ml-2 font-mono">
                                    ${(entry.value || 0).toFixed(2)}
                                  </span>
                                </p>
                              ))}
                            </div>
                            <div className="border-t border-gray-200 dark:border-gray-600 mt-2 pt-2">
                              <p className="text-gray-900 dark:text-gray-100 text-sm font-semibold flex justify-between">
                                <span>Total:</span>
                                <span className="font-mono">
                                  $
                                  {sortedEntries
                                    .reduce((sum, entry) => sum + (entry.value || 0), 0)
                                    .toFixed(2)}
                                </span>
                              </p>
                            </div>
                          </div>
                        )
                      }
                      return null
                    }}
                  />
                  <Legend />
                  {costSummary.regionBreakdown.map((region, index) => {
                    const colors = [
                      '#3b82f6', // blue
                      '#ef4444', // red
                      '#10b981', // green
                      '#f59e0b', // yellow
                      '#8b5cf6', // purple
                      '#06b6d4', // cyan
                      '#f97316', // orange
                      '#84cc16', // lime
                      '#ec4899', // pink
                      '#6366f1', // indigo
                    ]
                    const color = colors[index % colors.length]

                    return (
                      <Area
                        key={region.region}
                        type="monotone"
                        dataKey={region.region}
                        stackId="1"
                        stroke={color}
                        fill={color}
                        fillOpacity={0.6}
                        strokeWidth={1}
                        name={region.region}
                      />
                    )
                  })}

                  {refAreaLeft && refAreaRight ? (
                    <ReferenceArea
                      x1={refAreaLeft}
                      x2={refAreaRight}
                      strokeOpacity={0.3}
                    />
                  ) : null}
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <div className="text-center py-8">
              <p className="text-gray-500 dark:text-gray-400">No chart data available</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
