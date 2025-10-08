import { useState, useEffect, useMemo } from 'react'
import { Calendar } from '@/components/ui/calendar'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { CalendarIcon, X, Download } from 'lucide-react'
import { format } from 'date-fns'
import { cn, downloadCSV, downloadJSON, generateCostsFilename } from '@/lib/utils'
import { useRegionCostFilters } from '@/stores/appStore'
import { useChartZoom } from '@/lib/useChartZoom'
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceArea,
} from 'recharts'

interface RegionCostsProps {
  region: {
    name: string
  }
  isActive?: boolean
}

export default function RegionCosts({ region, isActive }: RegionCostsProps) {
  const [isLoading, setIsLoading] = useState(false)
  const [costsResponse, setCostsResponse] = useState<any>(null)
  const [error, setError] = useState<string | null>(null)
  const [selectedService, setSelectedService] = useState<string>('')
  const [selectedTableService, setSelectedTableService] = useState<string>('')
  const [selectedCostType, setSelectedCostType] = useState<string>('unblended_cost')
  const [defaultsSet, setDefaultsSet] = useState(false)

  // Region-specific state from Zustand
  const { startDate, endDate, activeCostsTab, setStartDate, setEndDate, setActiveCostsTab } =
    useRegionCostFilters(region.name)

  // Set default dates from metadata when data is first loaded
  useEffect(() => {
    if (defaultsSet || !costsResponse?.metadata) return

    const metaStartDate = costsResponse.metadata.start_date
    const metaEndDate = costsResponse.metadata.end_date

    // Only set defaults if both dates are valid and no user selection has been made
    if (
      !startDate &&
      !endDate &&
      metaStartDate &&
      metaEndDate &&
      !isNaN(new Date(metaStartDate).getTime()) &&
      !isNaN(new Date(metaEndDate).getTime())
    ) {
      setStartDate(new Date(metaStartDate))
      setEndDate(new Date(metaEndDate))
      setDefaultsSet(true)
    }
  }, [costsResponse, defaultsSet, startDate, endDate, setStartDate, setEndDate])

  // Custom reset functions that use metadata dates
  const resetToMetadataDates = () => {
    if (costsResponse?.metadata) {
      const metaStartDate = costsResponse.metadata.start_date
      const metaEndDate = costsResponse.metadata.end_date

      if (metaStartDate && metaEndDate) {
        setStartDate(new Date(metaStartDate))
        setEndDate(new Date(metaEndDate))
        resetZoom() // Reset chart zoom when dates are reset
      }
    }
  }

  const resetStartDateToMetadata = () => {
    if (costsResponse?.metadata?.start_date) {
      setStartDate(new Date(costsResponse.metadata.start_date))
      resetZoom() // Reset chart zoom when start date is reset
    }
  }

  const resetEndDateToMetadata = () => {
    if (costsResponse?.metadata?.end_date) {
      setEndDate(new Date(costsResponse.metadata.end_date))
      resetZoom() // Reset chart zoom when end date is reset
    }
  }

  // Process costs data for table, CSV, and chart formats using backend aggregates
  const processedData = useMemo(() => {
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
    costs.forEach((cost: any) => {
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
        const serviceAggregates = aggregates[service] as Record<string, any>

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
            if (usageTypeAggregate?.sum !== undefined) {
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
    costs.forEach((cost: any) => {
      if (!cost || !cost.service || !cost.usage_type || !cost.start || !cost.values) return

      // Skip usage_quantity cost type
      if (selectedCostType === 'usage_quantity') return

      const service = cost.service
      const usageType = cost.usage_type
      const date = cost.start
      const value = parseFloat(cost.values[selectedCostType]) || 0

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
    const tableData: any[] = []
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
      `Total (${selectedCostType.replace(/_/g, ' ').replace(/\b\w/g, (l) => l.toUpperCase())})`,
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
    const chartData = uniqueDates.map((date) => {
      const dateObj = new Date(date)
      const dataPoint: any = {
        date: date,
        formattedDate: dateObj.toLocaleDateString('en-US', {
          month: 'short',
          day: 'numeric',
        }),
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
    initialData: processedData.chartData,
    dataKey: 'epochTime',
    isNumericAxis: true,
    onDateRangeChange: (startDate, endDate) => {
      setStartDate(startDate)
      setEndDate(endDate)
    },
  })

  // Update zoom data when processedData changes
  useEffect(() => {
    updateData(processedData.chartData)
  }, [processedData.chartData, updateData])

  // Set first service as default when data loads
  useEffect(() => {
    if (processedData.services.length > 0 && !selectedTableService) {
      setSelectedTableService(processedData.services[0])
    }
  }, [processedData.services, selectedTableService])

  const handleDownloadCSV = () => {
    const filename = generateCostsFilename(region.name)
    downloadCSV(processedData.csvData, filename)
  }

  const handleDownloadJSON = () => {
    const filename = generateCostsFilename(region.name)
    downloadJSON(costsResponse, filename)
  }

  // Fetch costs when component becomes active or dates change
  useEffect(() => {
    if (!isActive || !region.name) {
      setIsLoading(false)
      return
    }

    const fetchCosts = async () => {
      setIsLoading(true)
      setError(null)

      try {
        console.log(`Fetching costs for region: ${region.name}`)

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
          throw new Error(`Failed to fetch costs: ${response.status} ${response.statusText}`)
        }

        const data = await response.json()
        setCostsResponse(data)
      } catch (err) {
        console.error('Error fetching costs:', err)
        setError(err instanceof Error ? err.message : 'Failed to fetch costs')
      } finally {
        setIsLoading(false)
      }
    }

    fetchCosts()
  }, [isActive, region.name, startDate, endDate, selectedCostType])

  // Set default selected service when data loads
  useEffect(() => {
    if (processedData.chartOptions.length > 0 && !selectedService) {
      setSelectedService(processedData.chartOptions[0].value)
    }
  }, [processedData.chartOptions, selectedService])

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
          <div className="flex items-center justify-center h-64">
            <div className="flex flex-col items-center space-y-4">
              <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500"></div>
              <p className="text-gray-500 dark:text-gray-400">Processing costs data...</p>
            </div>
          </div>
        </div>
      </div>
    )
  }

  // Show error state
  if (error) {
    return (
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          Region Costs
        </h3>
        <div className="text-red-500 dark:text-red-400">
          <p className="font-medium">Error loading costs:</p>
          <p className="text-sm mt-1">{error}</p>
        </div>
      </div>
    )
  }

  // Main component render
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
      {/* Filters: Cost Type, Service, and Date Picker Controls */}
      <div className="flex flex-col gap-4 mb-6">
        {/* Top row: Service and Cost Type Selectors */}
        <div className="flex flex-col sm:flex-row gap-4">
          {/* Service Selector */}
          <div className="flex flex-col space-y-2">
            <label className="text-sm font-medium text-gray-700 dark:text-gray-300">Service</label>
            <Select
              value={selectedService}
              onValueChange={setSelectedService}
            >
              <SelectTrigger className="w-[300px]">
                <SelectValue placeholder="Select service for chart" />
              </SelectTrigger>
              <SelectContent>
                {processedData.chartOptions.map((option) => (
                  <SelectItem
                    key={option.value}
                    value={option.value}
                  >
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Cost Type Selector */}
          <div className="flex flex-col space-y-2">
            <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Cost Type
            </label>
            <Select
              value={selectedCostType}
              onValueChange={setSelectedCostType}
            >
              <SelectTrigger className="w-[300px]">
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

        {/* Date Picker Controls */}
        <div className="flex flex-col sm:flex-row gap-4">
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
                  <X className="h-4 w-4 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" />
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

      {/* Results Section */}
      {error && (
        <div className="mb-4 p-4 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg">
          <div className="text-red-500 dark:text-red-400">
            <p className="font-medium">Error loading costs:</p>
            <p className="text-sm mt-1">{error}</p>
          </div>
        </div>
      )}

      {costsResponse && (
        <Tabs
          value={activeCostsTab}
          onValueChange={setActiveCostsTab}
          className="w-full max-w-full"
        >
          <div className="flex items-center justify-between mb-4">
            <TabsList className="grid w-auto grid-cols-4 gap-2 bg-gray-100 dark:bg-gray-700 p-1">
              <TabsTrigger
                value="chart"
                className="data-[state=active]:bg-white data-[state=active]:shadow-sm dark:data-[state=active]:bg-gray-800"
              >
                Chart
              </TabsTrigger>
              <TabsTrigger
                value="table"
                className="data-[state=active]:bg-white data-[state=active]:shadow-sm dark:data-[state=active]:bg-gray-800"
              >
                Table
              </TabsTrigger>
              <TabsTrigger
                value="json"
                className="data-[state=active]:bg-white data-[state=active]:shadow-sm dark:data-[state=active]:bg-gray-800"
              >
                JSON
              </TabsTrigger>
              <TabsTrigger
                value="csv"
                className="data-[state=active]:bg-white data-[state=active]:shadow-sm dark:data-[state=active]:bg-gray-800"
              >
                CSV
              </TabsTrigger>
            </TabsList>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={handleDownloadJSON}
                className="flex items-center gap-2"
              >
                <Download className="h-4 w-4" />
                JSON
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={handleDownloadCSV}
                className="flex items-center gap-2"
              >
                <Download className="h-4 w-4" />
                CSV
              </Button>
            </div>
          </div>

          <TabsContent
            value="chart"
            className="space-y-4 min-w-0"
          >
            <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 min-w-0 max-w-full">
              <div className="p-6 rounded-lg">
                {processedData.chartData.length > 0 && processedData.chartOptions.length > 0 ? (
                  <div className="space-y-6">
                    {/* Service Total Display */}
                    {selectedService && (
                      <div className="flex items-center justify-center mb-4">
                        <div className="flex items-center gap-4">
                          <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                            Service Total for {selectedService} (
                            {selectedCostType
                              .replace(/_/g, ' ')
                              .replace(/\b\w/g, (l) => l.toUpperCase())}
                            ):
                          </span>
                          <span className="text-lg font-bold text-green-600 dark:text-green-400">
                            $
                            {(
                              (processedData.serviceTotals as Record<string, number>)[
                                selectedService
                              ] || 0
                            ).toFixed(2)}
                          </span>
                        </div>
                      </div>
                    )}

                    {/* Stacked Area Chart for Usage Types */}
                    {selectedService && (
                      <div style={{ userSelect: 'none' }}>
                        <ResponsiveContainer
                          width="100%"
                          height={400}
                        >
                          <AreaChart
                            data={zoomData}
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
                            />
                            <Tooltip
                              cursor={{
                                stroke: '#8884d8',
                                strokeWidth: 2,
                                strokeDasharray: '5 5',
                              }}
                              content={({ active, payload, label }) => {
                                if (active && payload && payload.length > 0) {
                                  // Show all non-zero usage types in the tooltip
                                  const nonZeroEntries = payload.filter(
                                    (entry) => entry.value && entry.value > 0
                                  )

                                  if (nonZeroEntries.length === 0) return null

                                  // Sort by value (descending) for better readability
                                  const sortedEntries = nonZeroEntries.sort(
                                    (a, b) => (b.value || 0) - (a.value || 0)
                                  )

                                  return (
                                    <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-lg p-3 shadow-lg max-w-xs">
                                      <p className="text-gray-700 dark:text-gray-200 text-sm font-medium mb-2">
                                        {label}
                                      </p>
                                      <div className="space-y-1">
                                        {sortedEntries.map((entry, index) => (
                                          <p
                                            key={index}
                                            className="text-gray-900 dark:text-gray-100 text-sm flex items-center"
                                          >
                                            <span
                                              className="inline-block w-3 h-3 rounded-full mr-2 flex-shrink-0"
                                              style={{ backgroundColor: entry.color }}
                                            ></span>
                                            <span className="font-medium truncate">
                                              {entry.name}:
                                            </span>
                                            <span className="ml-auto pl-2 font-mono">
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
                            {/* Generate an Area for each usage type in the selected service */}
                            {processedData
                              .getUsageTypesForService(selectedService)
                              .map((usageType, index) => {
                                const usageKey = `${selectedService}:${usageType}`
                                const colors = [
                                  '#3b82f6',
                                  '#ef4444',
                                  '#10b981',
                                  '#f59e0b',
                                  '#8b5cf6',
                                  '#06b6d4',
                                  '#f97316',
                                  '#84cc16',
                                  '#ec4899',
                                  '#6366f1',
                                ]
                                const color = colors[index % colors.length]

                                return (
                                  <Area
                                    key={usageKey}
                                    type="monotone"
                                    dataKey={usageKey}
                                    stackId="1"
                                    stroke={color}
                                    fill={color}
                                    fillOpacity={0.6}
                                    strokeWidth={1}
                                    name={usageType}
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
                    )}
                  </div>
                ) : (
                  <div className="text-center py-8">
                    <p className="text-gray-500 dark:text-gray-400">No chart data available</p>
                  </div>
                )}
              </div>
            </div>
          </TabsContent>

          <TabsContent
            value="table"
            className="space-y-4 min-w-0"
          >
            {/* Service Filter for Table */}
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-4">
                <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
                  Filter by Service:
                </label>
                <Select
                  value={selectedTableService}
                  onValueChange={setSelectedTableService}
                >
                  <SelectTrigger className="w-[300px]">
                    <SelectValue placeholder="Choose a service to filter" />
                  </SelectTrigger>
                  <SelectContent>
                    {processedData.services.map((service) => (
                      <SelectItem
                        key={service}
                        value={service}
                      >
                        {service}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>

              <div className="flex items-center gap-6">
                <div className="flex items-center gap-2">
                  <span className="text-sm text-gray-500 dark:text-gray-400">
                    Total (
                    {selectedCostType.replace(/_/g, ' ').replace(/\b\w/g, (l) => l.toUpperCase())}):
                  </span>
                  <span className="text-lg font-bold text-green-600 dark:text-green-400">
                    $
                    {(
                      processedData.filteredTableData?.reduce((sum, row) => {
                        return sum + (row.total || 0)
                      }, 0) || 0
                    ).toFixed(2)}
                  </span>
                </div>
              </div>
            </div>

            <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 min-w-0 max-w-full">
              <div className="w-full overflow-hidden rounded-lg">
                <div className="overflow-x-auto max-h-96 overflow-y-auto">
                  <Table className="min-w-full">
                    <TableHeader>
                      <TableRow>
                        <TableHead className="sticky left-0 bg-white dark:bg-gray-800 z-10 w-[150px] max-w-[150px] border-r border-gray-200 dark:border-gray-600">
                          Service
                        </TableHead>
                        <TableHead className="sticky left-[150px] bg-white dark:bg-gray-800 z-10 w-[250px] max-w-[250px] border-r border-gray-200 dark:border-gray-600">
                          Usage Type
                        </TableHead>
                        <TableHead className="text-center w-[120px] min-w-[120px] max-w-[120px] border-r border-gray-200 dark:border-gray-600">
                          <div className="text-green-600 dark:text-green-400 font-semibold">
                            Total (
                            {selectedCostType
                              .replace(/_/g, ' ')
                              .replace(/\b\w/g, (l) => l.toUpperCase())}
                            )
                          </div>
                        </TableHead>
                        {processedData.uniqueDates.map((date, index) => (
                          <TableHead
                            key={index}
                            className="text-center w-[120px] min-w-[120px] max-w-[120px] border-r border-gray-200 dark:border-gray-600"
                          >
                            <div className="truncate">{date}</div>
                          </TableHead>
                        ))}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {(processedData.filteredTableData || []).map((row, rowIndex) => (
                        <TableRow
                          key={rowIndex}
                          className="hover:bg-gray-50 dark:hover:bg-gray-700"
                        >
                          <TableCell className="sticky left-0 bg-white dark:bg-gray-800 z-10 font-medium border-r border-gray-200 dark:border-gray-600 w-[150px] max-w-[150px]">
                            <div
                              className="truncate pr-2"
                              title={row.service}
                            >
                              {row.service}
                            </div>
                          </TableCell>

                          <TableCell className="sticky left-[150px] bg-white dark:bg-gray-800 z-10 border-r border-gray-200 dark:border-gray-600 w-[250px] max-w-[250px]">
                            <div
                              className="truncate pr-2 text-sm"
                              title={row.usageType}
                            >
                              {row.usageType}
                            </div>
                          </TableCell>

                          {/* Total column */}
                          <TableCell className="text-center border-r border-gray-200 dark:border-gray-600 w-[120px] min-w-[120px] max-w-[120px]">
                            <div className="font-mono text-sm truncate text-green-600 dark:text-green-400 font-semibold">
                              ${row.total.toFixed(2)}
                            </div>
                          </TableCell>

                          {/* Daily cost columns */}
                          {row.values.map((value: number, valueIndex: number) => (
                            <TableCell
                              key={valueIndex}
                              className="text-center border-r border-gray-200 dark:border-gray-600 w-[120px] min-w-[120px] max-w-[120px]"
                            >
                              <div className="font-mono text-sm truncate">${value.toFixed(2)}</div>
                            </TableCell>
                          ))}
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </div>
            </div>
          </TabsContent>

          <TabsContent
            value="json"
            className="space-y-4 min-w-0"
          >
            <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 min-w-0 max-w-full">
              <div className="flex items-center mb-2">
                <div className="flex-1" />
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    navigator.clipboard.writeText(JSON.stringify(costsResponse, null, 2))
                  }
                  className="text-xs flex-shrink-0"
                >
                  Copy JSON
                </Button>
              </div>
              <div className="w-full overflow-hidden">
                <pre className="text-xs text-gray-800 dark:text-gray-200 overflow-auto max-h-96 bg-white dark:bg-gray-800 p-4 rounded border max-w-full">
                  {JSON.stringify(costsResponse, null, 2)}
                </pre>
              </div>
            </div>
          </TabsContent>

          <TabsContent
            value="csv"
            className="space-y-4 min-w-0"
          >
            <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 min-w-0 max-w-full">
              <div className="flex items-center mb-2">
                <div className="flex-1" />
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => navigator.clipboard.writeText(processedData.csvData)}
                  className="text-xs flex-shrink-0"
                >
                  Copy CSV
                </Button>
              </div>
              <div className="w-full overflow-hidden">
                <pre className="text-xs text-gray-800 dark:text-gray-200 overflow-auto max-h-96 bg-white dark:bg-gray-800 p-4 rounded border font-mono max-w-full">
                  {processedData.csvData}
                </pre>
              </div>
            </div>
          </TabsContent>
        </Tabs>
      )}

      {!costsResponse && !error && !isLoading && (
        <div className="text-center py-8">
          <p className="text-gray-500 dark:text-gray-400">
            Select dates and fetch costs to view data for this region.
          </p>
        </div>
      )}
    </div>
  )
}
