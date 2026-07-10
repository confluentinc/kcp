import { useMemo, useState } from 'react'
import { generateMetricsFilename } from '@/lib/utils'
import { scopeConnectMetricsToConnector } from '@/lib/connectMetrics'
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
  sourceType: 'msk' | 'osk'
  kind?: 'self-managed' | 'managed'
  // When set (MSK-managed only), scope the metrics view to this connector and
  // strip the connector suffix from metric labels.
  connectorName?: string
  connectMetricsMetadata?: {
    start_date?: string
    end_date?: string
    period?: number
    metrics_source?: string
  }
}

export const ConnectMetrics = ({
  clusterId,
  sourceType,
  kind = 'self-managed',
  connectorName,
  connectMetricsMetadata,
}: ConnectMetricsProps) => {
  const [startDate, setStartDate] = useState<Date | undefined>(undefined)
  const [endDate, setEndDate] = useState<Date | undefined>(undefined)
  const [activeTab, setActiveTab] = useState<TabId>(TAB_IDS.CHART)

  const { metricsResponse: rawResponse, isLoading, error } = useConnectMetricsFetch({
    clusterId,
    sourceType,
    startDate,
    endDate,
    kind,
  })

  // For MSK-managed metrics, scope the (all-connectors) response to the selected
  // connector and strip the " (connector)" label suffix so every downstream view
  // (chart/table/query, JSON/CSV, aggregates) is connector-specific with bare
  // metric names. Self-managed passes no connectorName → response unchanged.
  const metricsResponse = useMemo(
    () =>
      connectorName && rawResponse
        ? scopeConnectMetricsToConnector(rawResponse, connectorName)
        : rawResponse,
    [rawResponse, connectorName]
  )

  // MSK-managed is scoped to a single connector, so it's "Connector Metrics";
  // self-managed is worker-level, so it stays "Connect Cluster Metrics".
  const heading = kind === 'managed' ? 'Connector Metrics' : 'Connect Cluster Metrics'
  const scanCommandHint =
    kind === 'managed'
      ? 'kcp scan msk-connectors --metrics cloudwatch'
      : 'kcp scan self-managed-connectors --metrics jolokia'

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
    filename: generateMetricsFilename(`${connectorName ?? clusterId}-connect`, ''),
  })

  return (
    <div className="bg-card rounded-lg border border-border p-6 transition-colors">
      <h4 className="text-lg font-semibold text-foreground mb-4">{heading}</h4>

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
            {kind === 'managed' ? (
              <>
                No Connect metrics data available. Run{' '}
                <code className="px-1.5 py-0.5 rounded bg-secondary text-sm">
                  {scanCommandHint}
                </code>{' '}
                to collect Connect metrics.
              </>
            ) : (
              <>
                No Connect metrics data available. Run{' '}
                <code className="px-1.5 py-0.5 rounded bg-secondary text-sm">
                  kcp scan self-managed-connectors --metrics jolokia
                </code>{' '}
                or{' '}
                <code className="px-1.5 py-0.5 rounded bg-secondary text-sm">
                  --metrics prometheus
                </code>{' '}
                to collect Connect metrics.
              </>
            )}
          </p>
        </div>
      )}
    </div>
  )
}
