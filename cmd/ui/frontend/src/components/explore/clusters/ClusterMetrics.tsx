import { useState, useEffect } from 'react'
import { Button } from '@/components/ui/button'
import Tabs from '@/components/common/Tabs'
import { Download } from 'lucide-react'
import { downloadCSV, downloadJSON, generateMetricsFilename } from '@/lib/utils'
import { useClusterDateFilters, useAppStore } from '@/stores/appStore'
import { useChartZoom } from '@/lib/useChartZoom'
import { useMetricsDataProcessor } from '@/hooks/useMetricsDataProcessor'
import { convertBytesToMB, getTCOFieldFromWorkloadAssumption } from '@/lib/metricsUtils'
import DateRangePicker from '@/components/common/DateRangePicker'
import MetricsChartTab from './MetricsChartTab'
import MetricsTableTab from './MetricsTableTab'
import MetricsCodeViewer from './MetricsCodeViewer'

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

  // Determine the TCO field based on the modal workload assumption
  const tcoField = modalWorkloadAssumption
    ? getTCOFieldFromWorkloadAssumption(modalWorkloadAssumption)
    : 'avgIngressThroughput'

  // Handle transferring values to TCO inputs
  const handleTransferToTCO = (value: number, statType: 'min' | 'avg' | 'max') => {
    const clusterKey = `${cluster.region || 'unknown'}:${cluster.name}`

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

  // Process metrics data using hook
  const processedData = useMetricsDataProcessor(metricsResponse)

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
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6 transition-colors">
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
    <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6 transition-colors">
      {/* Date Picker Controls */}
      <DateRangePicker
        startDate={startDate}
        endDate={endDate}
        onStartDateChange={setStartDate}
        onEndDateChange={setEndDate}
        onResetStartDate={resetStartDateToMetadata}
        onResetEndDate={resetEndDateToMetadata}
        onResetBoth={resetToMetadataDates}
        showResetBothButton={true}
        className="mb-6"
      />

      {/* Results Section */}
      {error && (
        <div className="mb-4 p-4 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-border rounded-lg">
          <div className="text-red-500 dark:text-red-400">
            <p className="font-medium">Error loading metrics:</p>
            <p className="text-sm mt-1">{error}</p>
          </div>
        </div>
      )}

      {metricsResponse && (
        <div className="w-full max-w-full">
          <div className="flex items-center justify-between mb-4">
            <Tabs
              tabs={[
                { id: 'chart', label: 'Chart' },
                { id: 'table', label: 'Table' },
                { id: 'json', label: 'JSON' },
                { id: 'csv', label: 'CSV' },
              ]}
              activeId={activeMetricsTab}
              onChange={(id) => setActiveMetricsTab(id as any)}
            />
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

          {activeMetricsTab === 'chart' && (
            <MetricsChartTab
              selectedMetric={selectedMetric}
              setSelectedMetric={setSelectedMetric}
              processedData={processedData}
              metricsResponse={metricsResponse}
              inModal={inModal}
              modalWorkloadAssumption={modalWorkloadAssumption}
              zoomData={zoomData}
              left={typeof left === 'number' ? left : undefined}
              right={typeof right === 'number' ? right : undefined}
              refAreaLeft={typeof refAreaLeft === 'number' ? refAreaLeft : undefined}
              refAreaRight={typeof refAreaRight === 'number' ? refAreaRight : undefined}
              handleMouseDown={handleMouseDown}
              handleMouseMove={handleMouseMove}
              zoom={zoom}
              transferSuccess={transferSuccess}
              handleTransferToTCO={handleTransferToTCO}
              tcoField={tcoField}
            />
          )}

          {activeMetricsTab === 'table' && (
            <MetricsTableTab
              processedData={processedData}
              metricsResponse={metricsResponse}
            />
          )}

          {activeMetricsTab === 'json' && (
            <MetricsCodeViewer
              data={JSON.stringify(metricsResponse, null, 2)}
              label="JSON"
              onCopy={() => navigator.clipboard.writeText(JSON.stringify(metricsResponse, null, 2))}
              isJSON={true}
            />
          )}

          {activeMetricsTab === 'csv' && (
            <MetricsCodeViewer
              data={processedData.csvData}
              label="CSV"
              onCopy={() => navigator.clipboard.writeText(processedData.csvData)}
              isJSON={false}
            />
          )}
        </div>
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
