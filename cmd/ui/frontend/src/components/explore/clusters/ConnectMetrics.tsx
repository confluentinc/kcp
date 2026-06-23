import { useState } from 'react'
import { generateMetricsFilename } from '@/lib/utils'
import { useMetricsDataProcessor } from '@/hooks/useMetricsDataProcessor'
import { useDateFilters } from '@/hooks/useDateFilters'
import { useMetricSelection } from '@/hooks/useMetricSelection'
import { useConnectMetricsFetch } from '@/hooks/useConnectMetricsFetch'
import { useClusterMetricsZoom } from '@/hooks/useClusterMetricsZoom'
import { useDownloadHandlers } from '@/hooks/useDownloadHandlers'
import { DateRangePicker } from '@/components/common/DateRangePicker'
import { DataViewTabs } from '@/components/common/DataViewTabs'
import { MetricsChartTab } from './MetricsChartTab'
import { MetricsTableTab } from './MetricsTableTab'
import { ClusterMetricsQueryTab } from './ClusterMetricsQueryTab'
import { TAB_IDS } from '@/constants'
import type { TabId } from '@/types'
import type { ApiMetadata } from '@/types/api/common'

interface ConnectMetricsProps {
  clusterId: string
  connectMetricsMetadata?: {
    start_date?: string
    end_date?: string
    period?: number
  }
}

export const ConnectMetrics = ({
  clusterId,
  connectMetricsMetadata,
}: ConnectMetricsProps) => {
  const [startDate, setStartDate] = useState<Date | undefined>(undefined)
  const [endDate, setEndDate] = useState<Date | undefined>(undefined)
  const [activeTab, setActiveTab] = useState<TabId>(TAB_IDS.CHART)

  const { metricsResponse, isLoading, error } = useConnectMetricsFetch({
    clusterId,
    startDate,
    endDate,
  })

  const processedData = useMetricsDataProcessor(metricsResponse)

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
    clusterName: `${clusterId}-connect`,
    clusterRegion: '',
    onDateRangeChange: (newStartDate, newEndDate) => {
      setStartDate(newStartDate)
      setEndDate(newEndDate)
    },
  })

  const { resetToMetadataDates, resetStartDateToMetadata, resetEndDateToMetadata } = useDateFilters(
    {
      startDate,
      endDate,
      setStartDate,
      setEndDate,
      metadata: (metricsResponse?.metadata || connectMetricsMetadata) as
        | ApiMetadata
        | null
        | undefined,
      onReset: resetZoom,
      autoSetDefaults: true,
    }
  )

  const { selectedMetric, setSelectedMetric } = useMetricSelection({
    availableMetrics: processedData.metrics,
    inModal: false,
    preselectedMetric: null,
    clusterName: `${clusterId}-connect`,
    clusterRegion: '',
  })

  const { handleDownloadCSV, handleDownloadJSON } = useDownloadHandlers({
    csvData: processedData.csvData,
    jsonData: metricsResponse,
    filename: generateMetricsFilename(`${clusterId}-connect`, ''),
  })

  return (
    <div className="bg-card rounded-lg border border-border p-6 transition-colors">
      <h4 className="text-lg font-semibold text-foreground mb-4">Connect Cluster Metrics</h4>

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
          activeTab={activeTab}
          onTabChange={(id) => setActiveTab(id)}
          onDownloadJSON={handleDownloadJSON}
          onDownloadCSV={handleDownloadCSV}
          jsonData={metricsResponse}
          csvData={processedData.csvData}
          renderChart={() => (
            <MetricsChartTab
              selectedMetric={selectedMetric}
              setSelectedMetric={setSelectedMetric}
              preselectedMetricMissing={false}
              processedData={processedData}
              metricsResponse={metricsResponse}
              inModal={false}
              zoomData={zoomData}
              left={typeof left === 'number' ? left : undefined}
              right={typeof right === 'number' ? right : undefined}
              refAreaLeft={typeof refAreaLeft === 'number' ? refAreaLeft : undefined}
              refAreaRight={typeof refAreaRight === 'number' ? refAreaRight : undefined}
              handleMouseDown={handleMouseDown}
              handleMouseMove={handleMouseMove}
              zoom={zoom}
              transferSuccess={null}
              handleTransferToTCO={() => {}}
              tcoField="avgIngressThroughput"
            />
          )}
          renderTable={() => (
            <MetricsTableTab
              processedData={processedData}
              metricsResponse={metricsResponse}
            />
          )}
          renderQuery={() => (
            <ClusterMetricsQueryTab queryInfo={metricsResponse?.query_info} />
          )}
        />
      )}

      {!metricsResponse && !error && !isLoading && (
        <div className="text-center py-8">
          <p className="text-muted-foreground">
            No Connect metrics data available. Run{' '}
            <code className="px-1.5 py-0.5 rounded bg-secondary text-sm">
              kcp scan self-managed-connectors --metrics jolokia
            </code>{' '}
            or{' '}
            <code className="px-1.5 py-0.5 rounded bg-secondary text-sm">
              --metrics prometheus
            </code>{' '}
            to collect Connect metrics.
          </p>
        </div>
      )}
    </div>
  )
}
