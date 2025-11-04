import { useState } from 'react'
import { generateMetricsFilename } from '@/lib/utils'
import { useClusterDateFilters, useAppStore } from '@/stores/store'
import { useMetricsDataProcessor } from '@/hooks/useMetricsDataProcessor'
import { useDateFilters } from '@/hooks/useDateFilters'
import { useMetricSelection } from '@/hooks/useMetricSelection'
import { useClusterMetricsFetch } from '@/hooks/useClusterMetricsFetch'
import { useClusterMetricsZoom } from '@/hooks/useClusterMetricsZoom'
import { useDownloadHandlers } from '@/hooks/useDownloadHandlers'
import { convertBytesToMB, getTCOFieldFromWorkloadAssumption } from '@/lib/metricsUtils'
import { DateRangePicker } from '@/components/common/DateRangePicker'
import { ErrorDisplay } from '@/components/common/ErrorDisplay'
import { DataViewTabs } from '@/components/common/DataViewTabs'
import { MetricsChartTab } from './MetricsChartTab'
import { MetricsTableTab } from './MetricsTableTab'
import type { ApiMetadata } from '@/types/api/common'

interface ClusterMetricsProps {
  cluster: {
    name: string
    region?: string
    arn: string // ARN is required - all clusters have ARNs
    metrics?: {
      metadata?: {
        start_date?: string
        end_date?: string
      }
    }
  }
  isActive?: boolean
  inModal?: boolean
  modalPreselectedMetric?: string
  modalWorkloadAssumption?: string
}

export const ClusterMetrics = ({
  cluster,
  isActive,
  inModal = false,
  modalPreselectedMetric,
  modalWorkloadAssumption,
}: ClusterMetricsProps) => {
  // Get TCO store actions and preselected metric
  const setTCOWorkloadValue = useAppStore((state) => state.setTCOWorkloadValue)
  const preselectedMetric = useAppStore((state) => state.preselectedMetric)
  const [transferSuccess, setTransferSuccess] = useState<string | null>(null)

  // Cluster-specific date state from Zustand (only used in non-modal mode)
  // Use ARN for cluster key (required for proper state management)
  const storeDateFilters = useClusterDateFilters(cluster.arn)

  // Modal date management - simple local state (not stored in Zustand)
  // useDateFilters hook handles all initialization and reset logic
  const [modalStartDate, setModalStartDate] = useState<Date | undefined>(undefined)
  const [modalEndDate, setModalEndDate] = useState<Date | undefined>(undefined)

  // Use local state in modal mode, store state otherwise
  const startDate = inModal ? modalStartDate : storeDateFilters.startDate
  const endDate = inModal ? modalEndDate : storeDateFilters.endDate
  const setStartDate = inModal ? setModalStartDate : storeDateFilters.setStartDate
  const setEndDate = inModal ? setModalEndDate : storeDateFilters.setEndDate

  // Fetch metrics data (using correct dates based on mode)
  const { metricsResponse, isLoading, error } = useClusterMetricsFetch({
    isActive: isActive ?? false,
    clusterName: cluster.name,
    clusterRegion: cluster.region || 'unknown',
    startDate: startDate,
    endDate: endDate,
  })

  // Process metrics data
  const processedData = useMetricsDataProcessor(metricsResponse)

  // Initialize chart zoom (now all date setters are available)
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
  } = useClusterMetricsZoom({
    chartData: processedData.chartData,
    clusterName: cluster.name,
    clusterRegion: cluster.region || 'unknown',
    onDateRangeChange: (newStartDate, newEndDate) => {
      setStartDate(newStartDate)
      setEndDate(newEndDate)
    },
  })

  // Use date filters hook with metadata for auto-initialization and reset functions
  // In modal mode, use cluster metadata; in non-modal mode, use response metadata
  const { resetToMetadataDates, resetStartDateToMetadata, resetEndDateToMetadata } = useDateFilters(
    {
      startDate,
      endDate,
      setStartDate,
      setEndDate,
      metadata: (inModal ? cluster.metrics?.metadata : metricsResponse?.metadata) as
        | ApiMetadata
        | null
        | undefined,
      onReset: resetZoom,
      autoSetDefaults: true, // Always auto-set from metadata
    }
  )

  // Metric selection with preselected metric support
  const { selectedMetric, setSelectedMetric } = useMetricSelection({
    availableMetrics: processedData.metrics,
    inModal,
    modalPreselectedMetric,
    preselectedMetric,
    clusterName: cluster.name,
    clusterRegion: cluster.region || 'unknown',
  })

  // Determine the TCO field based on the modal workload assumption
  const tcoField = modalWorkloadAssumption
    ? getTCOFieldFromWorkloadAssumption(modalWorkloadAssumption)
    : 'avgIngressThroughput'

  // Handle transferring values to TCO inputs
  const handleTransferToTCO = (value: number, statType: 'min' | 'avg' | 'max') => {
    // Convert bytes to MB for throughput metrics, but use raw value for partitions
    const convertedValue =
      tcoField === 'partitions' ? Math.round(value).toString() : convertBytesToMB(value)

    setTCOWorkloadValue(cluster.arn, tcoField, convertedValue)

    // Show success feedback with stat type
    setTransferSuccess(`${tcoField}-${statType}`)
    setTimeout(() => setTransferSuccess(null), 500)
  }

  // Active tab state from Zustand
  const activeMetricsTab = useAppStore((state) => state.activeMetricsTab)
  const setActiveMetricsTab = useAppStore((state) => state.setActiveMetricsTab)

  // Download handlers
  const { handleDownloadCSV, handleDownloadJSON } = useDownloadHandlers({
    csvData: processedData.csvData,
    jsonData: metricsResponse,
    filename: generateMetricsFilename(cluster.name, cluster.region),
  })

  // Show error state
  if (error) {
    return (
      <ErrorDisplay
        title="Cluster Metrics"
        error={error}
        context="metrics"
      />
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
        <DataViewTabs
          activeTab={activeMetricsTab}
          onTabChange={(id) => setActiveMetricsTab(id)}
          onDownloadJSON={handleDownloadJSON}
          onDownloadCSV={handleDownloadCSV}
          jsonData={metricsResponse}
          csvData={processedData.csvData}
          renderChart={() => (
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
          renderTable={() => (
            <MetricsTableTab
              processedData={processedData}
              metricsResponse={metricsResponse}
            />
          )}
        />
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
