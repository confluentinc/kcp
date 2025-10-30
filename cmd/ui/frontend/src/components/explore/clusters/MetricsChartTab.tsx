import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Line } from 'recharts'
import DateRangeChart, { SimpleChartTooltip } from '@/components/charts/DateRangeChart'
import MetricsAggregateStats from './MetricsAggregateStats'
import { getWorkloadAssumptionName } from '@/lib/metricsUtils'

interface ProcessedData {
  chartData: Array<{
    date: string
    formattedDate: string
    epochTime: number
    [key: string]: string | number | null | undefined
  }>
  metrics: string[]
}

interface MetricsChartTabProps {
  selectedMetric: string
  setSelectedMetric: (metric: string) => void
  processedData: ProcessedData
  metricsResponse: {
    aggregates?: Record<string, { min?: number; avg?: number; max?: number }>
  }
  inModal: boolean
  modalWorkloadAssumption?: string
  zoomData: any[]
  left: number | undefined
  right: number | undefined
  refAreaLeft: number | undefined
  refAreaRight: number | undefined
  handleMouseDown: (e: any) => void
  handleMouseMove: (e: any) => void
  zoom: () => void
  transferSuccess: string | null
  handleTransferToTCO: (value: number, statType: 'min' | 'avg' | 'max') => void
  tcoField: string
}

export default function MetricsChartTab({
  selectedMetric,
  setSelectedMetric,
  processedData,
  metricsResponse,
  inModal,
  modalWorkloadAssumption,
  zoomData,
  left,
  right,
  refAreaLeft,
  refAreaRight,
  handleMouseDown,
  handleMouseMove,
  zoom,
  transferSuccess,
  handleTransferToTCO,
  tcoField,
}: MetricsChartTabProps) {
  return (
    <div className="space-y-4 min-w-0">
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
                  <MetricsAggregateStats
                    aggregates={metricsResponse.aggregates}
                    selectedMetric={selectedMetric}
                    inModal={inModal}
                    onTransfer={handleTransferToTCO}
                    transferSuccess={transferSuccess}
                    tcoField={tcoField}
                  />
                )}
              </div>

              {/* Single Chart */}
              {selectedMetric && (
                <DateRangeChart
                  data={processedData.chartData}
                  chartType="line"
                  height={400}
                  customTooltip={(props) => (
                    <SimpleChartTooltip
                      {...props}
                      labelKey={selectedMetric}
                    />
                  )}
                  zoomData={zoomData}
                  left={typeof left === 'number' ? left : undefined}
                  right={typeof right === 'number' ? right : undefined}
                  refAreaLeft={typeof refAreaLeft === 'number' ? refAreaLeft : undefined}
                  refAreaRight={typeof refAreaRight === 'number' ? refAreaRight : undefined}
                  onMouseDown={handleMouseDown}
                  onMouseMove={handleMouseMove}
                  onMouseUp={zoom}
                >
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
                </DateRangeChart>
              )}
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
