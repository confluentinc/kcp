import { useState } from 'react'
import { Button } from '@/components/common/ui/button'
import Tabs from '@/components/common/Tabs'
import { Download } from 'lucide-react'
import { downloadCSV, downloadJSON, generateMetricsFilename } from '@/lib/utils'
import { useClusterDateFilters, useAppStore } from '@/stores/store'
import { useMetricsDataProcessor } from '@/hooks/useMetricsDataProcessor'
import { useDateFilters } from '@/hooks/useDateFilters'
import { useModalMetricsDates } from '@/hooks/useModalMetricsDates'
import { useMetricSelection } from '@/hooks/useMetricSelection'
import { useClusterMetricsFetch } from '@/hooks/useClusterMetricsFetch'
import { useClusterMetricsZoom } from '@/hooks/useClusterMetricsZoom'
import { convertBytesToMB, getTCOFieldFromWorkloadAssumption } from '@/lib/metricsUtils'
import DateRangePicker from '@/components/common/DateRangePicker'
import MetricsChartTab from './MetricsChartTab'
import MetricsTableTab from './MetricsTableTab'
import MetricsCodeViewer from './MetricsCodeViewer'
import { TAB_IDS } from '@/constants'
import type { TabId } from '@/types'
import type { ApiMetadata } from '@/types/api/common'

interface ClusterMetricsProps {
  cluster: {
    name: string
    region?: string
    arn?: string
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

export default function ClusterMetrics({
  cluster,
  isActive,
  inModal = false,
  modalPreselectedMetric,
  modalWorkloadAssumption,
}: ClusterMetricsProps) {
  // Get TCO store actions and preselected metric
  const setTCOWorkloadValue = useAppStore((state) => state.setTCOWorkloadValue)
  const preselectedMetric = useAppStore((state) => state.preselectedMetric)
  const [transferSuccess, setTransferSuccess] = useState<string | null>(null)

  // Cluster-specific date state from Zustand (only used in non-modal mode)
  // Use ARN if available, otherwise fall back to region-name combo for backward compatibility
  const storeDateFilters = useClusterDateFilters(cluster.arn || `${cluster.region || 'unknown'}-${cluster.name}`)

  // Modal date management with separate hook
  const { modalStartDate, modalEndDate, setModalStartDate, setModalEndDate } = useModalMetricsDates(
    {
      inModal,
      isActive: isActive ?? false,
      clusterName: cluster.name,
      clusterRegion: cluster.region || 'unknown',
      metricsResponseMetadata: undefined, // Will be set after first fetch
    }
  )

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
      metadata: (inModal ? cluster.metrics?.metadata : metricsResponse?.metadata) as ApiMetadata | null | undefined,
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
    // Use ARN if available, otherwise fall back to region:name format
    const clusterKey = cluster.arn || `${cluster.region || 'unknown'}:${cluster.name}`

    // Convert bytes to MB for throughput metrics, but use raw value for partitions
    const convertedValue =
      tcoField === 'partitions' ? Math.round(value).toString() : convertBytesToMB(value)

    setTCOWorkloadValue(clusterKey, tcoField, convertedValue)

    // Show success feedback with stat type
    setTransferSuccess(`${tcoField}-${statType}`)
    setTimeout(() => setTransferSuccess(null), 500)
  }

  // Active tab state from Zustand
  const activeMetricsTab = useAppStore((state) => state.activeMetricsTab)
  const setActiveMetricsTab = useAppStore((state) => state.setActiveMetricsTab)

  // Download handlers
  const handleDownloadCSV = () => {
    const filename = generateMetricsFilename(cluster.name, cluster.region)
    downloadCSV(processedData.csvData, filename)
  }

  const handleDownloadJSON = () => {
    const filename = generateMetricsFilename(cluster.name, cluster.region)
    downloadJSON(metricsResponse, filename)
  }

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
                { id: TAB_IDS.CHART, label: 'Chart' },
                { id: TAB_IDS.TABLE, label: 'Table' },
                { id: TAB_IDS.JSON, label: 'JSON' },
                { id: TAB_IDS.CSV, label: 'CSV' },
              ]}
              activeId={activeMetricsTab}
              onChange={(id) => setActiveMetricsTab(id as TabId)}
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

          {activeMetricsTab === TAB_IDS.CHART && (
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

          {activeMetricsTab === TAB_IDS.TABLE && (
            <MetricsTableTab
              processedData={processedData}
              metricsResponse={metricsResponse}
            />
          )}

          {activeMetricsTab === TAB_IDS.JSON && (
            <MetricsCodeViewer
              data={JSON.stringify(metricsResponse, null, 2)}
              label="JSON"
              onCopy={() => navigator.clipboard.writeText(JSON.stringify(metricsResponse, null, 2))}
              isJSON={true}
            />
          )}

          {activeMetricsTab === TAB_IDS.CSV && (
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
