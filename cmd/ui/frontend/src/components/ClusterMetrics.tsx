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
import { cn, downloadCSV, downloadJSON, generateMetricsFilename } from '@/lib/utils'
import { useClusterDateFilters, useAppStore } from '@/stores/appStore'
import { useChartZoom } from '@/lib/useChartZoom'
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceArea,
} from 'recharts'

interface ClusterMetricsProps {
  cluster: {
    name: string
    region?: string
  }
  isActive?: boolean
  inModal?: boolean
  modalPreselectedMetric?: string
  modalWorkloadAssumption?: string
}

export default function ClusterMetrics({
  cluster,
  isActive,
  inModal = false,
  modalPreselectedMetric,
  modalWorkloadAssumption,
}: ClusterMetricsProps) {
  const [isLoading, setIsLoading] = useState(false)
  const [metricsResponse, setMetricsResponse] = useState<any>(null)
  const [error, setError] = useState<string | null>(null)
  const [selectedMetric, setSelectedMetric] = useState<string>('')
  const [defaultsSet, setDefaultsSet] = useState(false)

  // Get TCO store actions and preselected metric
  const { setTCOWorkloadValue, preselectedMetric } = useAppStore()
  const [hasUsedPreselectedMetric, setHasUsedPreselectedMetric] = useState(false)
  const [transferSuccess, setTransferSuccess] = useState<string | null>(null)

  // Reset preselected metric flag when cluster changes
  useEffect(() => {
    setHasUsedPreselectedMetric(false)
  }, [cluster.name, cluster.region])

  // Convert bytes/sec to MB/s
  const convertBytesToMB = (bytesPerSec: number): string => {
    const mbPerSec = bytesPerSec / (1024 * 1024)
    return mbPerSec.toFixed(5)
  }

  // Map metric names to workload assumption names
  const getWorkloadAssumptionName = (metricName: string): string => {
    switch (metricName) {
      case 'BytesInPerSec':
        return 'Ingress Throughput'
      case 'BytesOutPerSec':
        return 'Egress Throughput'
      case 'GlobalPartitionCount':
        return 'Partitions'
      default:
        return 'Metric'
    }
  }

  // Map modal workload assumption to TCO field
  const getTCOFieldFromWorkloadAssumption = (workloadAssumption: string): string => {
    switch (workloadAssumption) {
      case 'Avg Ingress Throughput (MB/s)':
        return 'avgIngressThroughput'
      case 'Peak Ingress Throughput (MB/s)':
        return 'peakIngressThroughput'
      case 'Avg Egress Throughput (MB/s)':
        return 'avgEgressThroughput'
      case 'Peak Egress Throughput (MB/s)':
        return 'peakEgressThroughput'
      case 'Partitions':
        return 'partitions'
      default:
        return 'avgIngressThroughput' // fallback
    }
  }

  // Handle transferring values to TCO inputs
  const handleTransferToTCO = (value: number, statType: 'min' | 'avg' | 'max') => {
    const clusterKey = `${cluster.region || 'unknown'}:${cluster.name}`

    // Determine the TCO field based on the modal workload assumption
    const tcoField = modalWorkloadAssumption
      ? getTCOFieldFromWorkloadAssumption(modalWorkloadAssumption)
      : 'avgIngressThroughput'

    // Convert bytes to MB for throughput metrics, but use raw value for partitions
    const convertedValue =
      tcoField === 'partitions' ? Math.round(value).toString() : convertBytesToMB(value)

    setTCOWorkloadValue(clusterKey, tcoField as any, convertedValue)

    // Show success feedback with stat type
    setTransferSuccess(`${tcoField}-${statType}`)
    setTimeout(() => setTransferSuccess(null), 500)
  }

  // Cluster-specific date state from Zustand
  const { startDate, endDate, setStartDate, setEndDate } = useClusterDateFilters(
    cluster.region || 'unknown',
    cluster.name
  )

  // Active tab state from Zustand
  const activeMetricsTab = useAppStore((state) => state.activeMetricsTab)
  const setActiveMetricsTab = useAppStore((state) => state.setActiveMetricsTab)

  // Set default dates from metadata when data is first loaded
  useEffect(() => {
    if (defaultsSet || !metricsResponse?.metadata) return

    const metaStartDate = metricsResponse.metadata.start_date
    const metaEndDate = metricsResponse.metadata.end_date

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
  }, [metricsResponse, defaultsSet, startDate, endDate, setStartDate, setEndDate])

  // Custom reset functions that use metadata dates
  const resetToMetadataDates = () => {
    if (metricsResponse?.metadata) {
      const metaStartDate = metricsResponse.metadata.start_date
      const metaEndDate = metricsResponse.metadata.end_date

      if (metaStartDate && metaEndDate) {
        setStartDate(new Date(metaStartDate))
        setEndDate(new Date(metaEndDate))
        resetZoom() // Reset chart zoom when dates are reset
      }
    }
  }

  const resetStartDateToMetadata = () => {
    if (metricsResponse?.metadata?.start_date) {
      setStartDate(new Date(metricsResponse.metadata.start_date))
      resetZoom() // Reset chart zoom when start date is reset
    }
  }

  const resetEndDateToMetadata = () => {
    if (metricsResponse?.metadata?.end_date) {
      setEndDate(new Date(metricsResponse.metadata.end_date))
      resetZoom() // Reset chart zoom when end date is reset
    }
  }

  // Process metrics data for table and CSV formats
  const processedData = useMemo(() => {
    if (!metricsResponse?.results || !Array.isArray(metricsResponse.results)) {
      return { tableData: [], csvData: '', chartData: [], uniqueDates: [], metrics: [] }
    }

    const metrics = metricsResponse.results

    // Get all unique dates and sort them
    const allDates = new Set<string>()
    metrics.forEach((metric: any) => {
      if (metric && metric.start && typeof metric.start === 'string') {
        allDates.add(metric.start.split('T')[0]) // Get date part only
      }
    })
    const uniqueDates = Array.from(allDates).sort()

    // Group metrics by label
    const metricsByLabel: Record<string, Record<string, number | null>> = {}
    metrics.forEach((metric: any) => {
      if (!metric || !metric.label) return

      if (!metricsByLabel[metric.label]) {
        metricsByLabel[metric.label] = {}
      }
      const date =
        metric.start && typeof metric.start === 'string' ? metric.start.split('T')[0] : ''
      if (date) {
        metricsByLabel[metric.label][date] = typeof metric.value === 'number' ? metric.value : null
      }
    })

    // Create table data
    const tableData = Object.keys(metricsByLabel).map((label) => ({
      metric: label,
      values: uniqueDates.map((date) => metricsByLabel[label][date] ?? null),
    }))

    // Create CSV data
    const csvHeaders = ['Metric', ...uniqueDates]
    const csvRows = Object.keys(metricsByLabel).map((label) => [
      label || '',
      ...uniqueDates.map((date) => {
        const value = metricsByLabel[label][date]
        return value !== null && value !== undefined && typeof value === 'number'
          ? value.toString()
          : ''
      }),
    ])
    const csvData = [csvHeaders, ...csvRows]
      .map((row) => row.map((cell) => `"${cell || ''}"`).join(','))
      .join('\n')

    // Create chart data
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

      Object.keys(metricsByLabel).forEach((label) => {
        const cleanLabel = label.replace('Cluster Aggregate - ', '')
        const value = metricsByLabel[label][date]
        dataPoint[cleanLabel] = value !== null && value !== undefined ? value : null
      })

      return dataPoint
    })

    return {
      tableData,
      csvData,
      chartData,
      uniqueDates,
      metrics: Object.keys(metricsByLabel).map((label) =>
        label.replace('Cluster Aggregate - ', '')
      ),
    }
  }, [metricsResponse])

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

  const handleDownloadCSV = () => {
    const filename = generateMetricsFilename(cluster.name, cluster.region)
    downloadCSV(processedData.csvData, filename)
  }

  const handleDownloadJSON = () => {
    const filename = generateMetricsFilename(cluster.name, cluster.region)
    downloadJSON(metricsResponse, filename)
  }

  // Fetch metrics when tab becomes active
  useEffect(() => {
    if (!isActive || !cluster.name) {
      setIsLoading(false)
      return
    }

    const fetchMetrics = async () => {
      setIsLoading(true)
      setError(null)

      try {
        // Use cluster.region if available, otherwise fallback to 'unknown'
        const region = cluster.region || 'unknown'
        const clusterName = cluster.name

        console.log(`Fetching metrics for region: ${region}, cluster: ${clusterName}`)

        // Build URL with optional date parameters
        let url = `/metrics/${region}/${clusterName}`
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
          throw new Error(`Failed to fetch metrics: ${response.status} ${response.statusText}`)
        }

        const data = await response.json()
        setMetricsResponse(data)
        console.log('Metrics response:', data)
      } catch (err) {
        console.error('Error fetching metrics:', err)
        setError(err instanceof Error ? err.message : 'Failed to fetch metrics')
      } finally {
        setIsLoading(false)
      }
    }

    fetchMetrics()
  }, [isActive, cluster.name, cluster.region, startDate, endDate])

  // Set default selected metric when data loads, prioritizing modal preselected metric
  useEffect(() => {
    if (processedData.metrics.length > 0) {
      // In modal mode, always use the modal preselected metric if provided
      if (
        inModal &&
        modalPreselectedMetric &&
        processedData.metrics.includes(modalPreselectedMetric)
      ) {
        setSelectedMetric(modalPreselectedMetric)
      } else if (
        !inModal &&
        preselectedMetric &&
        processedData.metrics.includes(preselectedMetric) &&
        !hasUsedPreselectedMetric
      ) {
        setSelectedMetric(preselectedMetric)
        setHasUsedPreselectedMetric(true)
      } else if (!selectedMetric) {
        setSelectedMetric(processedData.metrics[0])
      }
    }
  }, [
    processedData.metrics,
    selectedMetric,
    preselectedMetric,
    hasUsedPreselectedMetric,
    inModal,
    modalPreselectedMetric,
  ])

  // Show error state
  if (error) {
    return (
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          Cluster Metrics
        </h3>
        <div className="text-red-500 dark:text-red-400">
          <p className="font-medium">Error loading metrics:</p>
          <p className="text-sm mt-1">{error}</p>
        </div>
      </div>
    )
  }

  // Main component render
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
      {/* Date Picker Controls */}
      <div className="flex flex-col sm:flex-row gap-4 mb-6">
        <div className="flex flex-col space-y-2">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">Start Date</label>
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
                title="Reset to default start date"
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
                title="Reset to default end date"
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

      {/* Results Section */}
      {error && (
        <div className="mb-4 p-4 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg">
          <div className="text-red-500 dark:text-red-400">
            <p className="font-medium">Error loading metrics:</p>
            <p className="text-sm mt-1">{error}</p>
          </div>
        </div>
      )}

      {metricsResponse && (
        <Tabs
          value={activeMetricsTab}
          onValueChange={setActiveMetricsTab}
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
                {processedData.chartData.length > 0 && processedData.metrics.length > 0 ? (
                  <div className="space-y-6">
                    {/* Metric Selector and Summary Stats */}
                    <div className="flex items-center justify-between">
                      {/* Left side: Metric Selector (hidden in modal mode) */}
                      {!inModal && (
                        <div className="flex items-center gap-4">
                          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
                            Select Metric:
                          </label>
                          <Select
                            value={selectedMetric}
                            onValueChange={setSelectedMetric}
                          >
                            <SelectTrigger className="w-[300px]">
                              <SelectValue placeholder="Choose a metric to visualize" />
                            </SelectTrigger>
                            <SelectContent>
                              {processedData.metrics.map((metric) => (
                                <SelectItem
                                  key={metric}
                                  value={metric}
                                >
                                  {metric}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </div>
                      )}

                      {/* In modal mode, show the selected metric as a title */}
                      {inModal && selectedMetric && (
                        <div className="flex items-center gap-4">
                          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
                            {selectedMetric} -{' '}
                            {modalWorkloadAssumption || getWorkloadAssumptionName(selectedMetric)}
                          </h3>
                        </div>
                      )}

                      {/* Right side: Aggregates Stats */}
                      {selectedMetric && metricsResponse?.aggregates && (
                        <div className="space-y-1">
                          {(() => {
                            // Find the metric in the aggregates data (now uses clean metric names)
                            const metricAggregate = metricsResponse.aggregates[selectedMetric]

                            if (!metricAggregate) {
                              return (
                                <span className="text-sm text-gray-500 dark:text-gray-400">
                                  No data available
                                </span>
                              )
                            }

                            return (
                              <>
                                {/* MIN Row */}
                                <div className="flex items-center justify-between">
                                  <div className="flex items-center gap-3">
                                    <span className="text-xs font-medium text-gray-700 dark:text-gray-300 uppercase w-8">
                                      MIN
                                    </span>
                                    <span className="text-sm font-semibold text-blue-600 dark:text-blue-400">
                                      {metricAggregate.min?.toFixed(2) ?? 'N/A'}
                                    </span>
                                  </div>
                                  <div className="ml-4">
                                    {inModal &&
                                    metricAggregate.min !== null &&
                                    metricAggregate.min !== undefined ? (
                                      <Button
                                        onClick={() =>
                                          handleTransferToTCO(metricAggregate.min, 'min')
                                        }
                                        variant="outline"
                                        size="sm"
                                        className="h-6 w-36 text-xs"
                                      >
                                        <span className="flex items-center justify-center gap-1">
                                          {transferSuccess?.includes('-min') && (
                                            <span className="text-green-600">✓</span>
                                          )}
                                          Use as TCO Input
                                        </span>
                                      </Button>
                                    ) : (
                                      <div className="w-36"></div>
                                    )}
                                  </div>
                                </div>

                                {/* AVG Row */}
                                <div className="flex items-center justify-between">
                                  <div className="flex items-center gap-3">
                                    <span className="text-xs font-medium text-gray-700 dark:text-gray-300 uppercase w-8">
                                      AVG
                                    </span>
                                    <span className="text-sm font-semibold text-green-600 dark:text-green-400">
                                      {metricAggregate.avg?.toFixed(2) ?? 'N/A'}
                                    </span>
                                  </div>
                                  <div className="ml-4">
                                    {inModal &&
                                    metricAggregate.avg !== null &&
                                    metricAggregate.avg !== undefined ? (
                                      <Button
                                        onClick={() =>
                                          handleTransferToTCO(metricAggregate.avg, 'avg')
                                        }
                                        variant="outline"
                                        size="sm"
                                        className="h-6 w-36 text-xs"
                                      >
                                        <span className="flex items-center justify-center gap-1">
                                          {transferSuccess?.includes('-avg') && (
                                            <span className="text-green-600">✓</span>
                                          )}
                                          Use as TCO Input
                                        </span>
                                      </Button>
                                    ) : (
                                      <div className="w-36"></div>
                                    )}
                                  </div>
                                </div>

                                {/* MAX Row */}
                                <div className="flex items-center justify-between">
                                  <div className="flex items-center gap-3">
                                    <span className="text-xs font-medium text-gray-700 dark:text-gray-300 uppercase w-8">
                                      MAX
                                    </span>
                                    <span className="text-sm font-semibold text-red-600 dark:text-red-400">
                                      {metricAggregate.max?.toFixed(2) ?? 'N/A'}
                                    </span>
                                  </div>
                                  <div className="ml-4">
                                    {inModal &&
                                    metricAggregate.max !== null &&
                                    metricAggregate.max !== undefined ? (
                                      <Button
                                        onClick={() =>
                                          handleTransferToTCO(metricAggregate.max, 'max')
                                        }
                                        variant="outline"
                                        size="sm"
                                        className="h-6 w-36 text-xs"
                                      >
                                        <span className="flex items-center justify-center gap-1">
                                          {transferSuccess?.includes('-max') && (
                                            <span className="text-green-600">✓</span>
                                          )}
                                          Use as TCO Input
                                        </span>
                                      </Button>
                                    ) : (
                                      <div className="w-36"></div>
                                    )}
                                  </div>
                                </div>
                              </>
                            )
                          })()}
                        </div>
                      )}
                    </div>

                    {/* Single Chart */}
                    {selectedMetric && (
                      <div style={{ userSelect: 'none' }}>
                        <ResponsiveContainer
                          width="100%"
                          height={400}
                        >
                          <LineChart
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
                              content={({ active, payload, label }) => {
                                if (active && payload && payload.length) {
                                  return (
                                    <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-lg p-3 shadow-lg">
                                      <p className="text-gray-700 dark:text-gray-200 text-sm font-medium mb-1">
                                        {label
                                          ? format(new Date(label), 'MMM dd, yyyy HH:mm')
                                          : 'Unknown Date'}
                                      </p>
                                      <p className="text-gray-900 dark:text-gray-100 text-sm">
                                        <span className="font-medium">{selectedMetric}:</span>{' '}
                                        {payload[0].value !== null ? payload[0].value : 'No data'}
                                      </p>
                                    </div>
                                  )
                                }
                                return null
                              }}
                            />
                            <Line
                              type="monotone"
                              dataKey={selectedMetric}
                              stroke="#3b82f6"
                              strokeWidth={3}
                              dot={{ r: 2, fill: '#3b82f6' }}
                              activeDot={{ r: 4, fill: '#1d4ed8' }}
                              connectNulls={false}
                              name={selectedMetric}
                            />

                            {refAreaLeft && refAreaRight ? (
                              <ReferenceArea
                                x1={refAreaLeft}
                                x2={refAreaRight}
                                strokeOpacity={0.3}
                              />
                            ) : null}
                          </LineChart>
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
            <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 min-w-0 max-w-full">
              <div className="w-full overflow-hidden rounded-lg">
                <div className="overflow-x-auto max-h-96 overflow-y-auto">
                  <Table className="min-w-full">
                    <TableHeader>
                      <TableRow>
                        <TableHead className="sticky left-0 bg-white dark:bg-gray-800 z-10 w-[200px] max-w-[200px] border-r border-gray-200 dark:border-gray-600">
                          Metric
                        </TableHead>
                        <TableHead className="text-center w-[100px] min-w-[100px] max-w-[100px] border-r border-gray-200 dark:border-gray-600">
                          <div className="text-blue-600 dark:text-blue-400 font-semibold">Min</div>
                        </TableHead>
                        <TableHead className="text-center w-[100px] min-w-[100px] max-w-[100px] border-r border-gray-200 dark:border-gray-600">
                          <div className="text-green-600 dark:text-green-400 font-semibold">
                            Avg
                          </div>
                        </TableHead>
                        <TableHead className="text-center w-[100px] min-w-[100px] max-w-[100px] border-r border-gray-200 dark:border-gray-600">
                          <div className="text-red-600 dark:text-red-400 font-semibold">Max</div>
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
                      {processedData.tableData.map((row, rowIndex) => {
                        // Get aggregate data for this metric
                        const cleanMetricName = row.metric.replace('Cluster Aggregate - ', '')
                        const metricAggregate = metricsResponse?.aggregates?.[cleanMetricName]

                        return (
                          <TableRow
                            key={rowIndex}
                            className="hover:bg-gray-50 dark:hover:bg-gray-700"
                          >
                            <TableCell className="sticky left-0 bg-white dark:bg-gray-800 z-10 font-medium border-r border-gray-200 dark:border-gray-600 w-[200px] max-w-[200px]">
                              <div
                                className="truncate pr-2"
                                title={row.metric}
                              >
                                {cleanMetricName}
                              </div>
                            </TableCell>

                            {/* Min column */}
                            <TableCell className="text-center border-r border-gray-200 dark:border-gray-600 w-[100px] min-w-[100px] max-w-[100px]">
                              <div className="font-mono text-sm truncate text-blue-600 dark:text-blue-400 font-semibold">
                                {metricAggregate?.min?.toFixed(2) ?? '-'}
                              </div>
                            </TableCell>

                            {/* Avg column */}
                            <TableCell className="text-center border-r border-gray-200 dark:border-gray-600 w-[100px] min-w-[100px] max-w-[100px]">
                              <div className="font-mono text-sm truncate text-green-600 dark:text-green-400 font-semibold">
                                {metricAggregate?.avg?.toFixed(2) ?? '-'}
                              </div>
                            </TableCell>

                            {/* Max column */}
                            <TableCell className="text-center border-r border-gray-200 dark:border-gray-600 w-[100px] min-w-[100px] max-w-[100px]">
                              <div className="font-mono text-sm truncate text-red-600 dark:text-red-400 font-semibold">
                                {metricAggregate?.max?.toFixed(2) ?? '-'}
                              </div>
                            </TableCell>

                            {/* Existing date columns */}
                            {row.values.map((value, valueIndex) => (
                              <TableCell
                                key={valueIndex}
                                className="text-center border-r border-gray-200 dark:border-gray-600 w-[120px] min-w-[120px] max-w-[120px]"
                              >
                                <div className="font-mono text-sm truncate">
                                  {value !== null ? value.toFixed(2) : '-'}
                                </div>
                              </TableCell>
                            ))}
                          </TableRow>
                        )
                      })}
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
                    navigator.clipboard.writeText(JSON.stringify(metricsResponse, null, 2))
                  }
                  className="text-xs flex-shrink-0"
                >
                  Copy JSON
                </Button>
              </div>
              <div className="w-full overflow-hidden">
                <pre className="text-xs text-gray-800 dark:text-gray-200 overflow-auto max-h-96 bg-white dark:bg-gray-800 p-4 rounded border max-w-full">
                  {JSON.stringify(metricsResponse, null, 2)}
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

      {!metricsResponse && !error && !isLoading && (
        <div className="text-center py-8">
          <p className="text-gray-500 dark:text-gray-400">
            Select dates and fetch metrics to view data for this cluster.
          </p>
        </div>
      )}
    </div>
  )
}
