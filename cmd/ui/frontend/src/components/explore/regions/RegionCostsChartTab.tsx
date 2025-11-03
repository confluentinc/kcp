import type { CategoricalChartFunc } from 'recharts/types/chart/types'
import { Area } from 'recharts'
import { DateRangeChart, CostChartTooltip, type ChartDataPoint } from '@/components/common/DateRangeChart'
import { formatCostTypeLabel } from '@/lib/costTypeUtils'
import { getChartColor } from '@/lib/chartColors'

interface ProcessedData {
  chartData: ChartDataPoint[]
  chartOptions: Array<{
    value: string
    label: string
    type: 'service'
  }>
  getUsageTypesForService: (serviceName: string) => string[]
  serviceTotals: Record<string, number>
}

interface RegionCostsChartTabProps {
  selectedService: string
  selectedCostType: string
  processedData: ProcessedData
  zoomData: ChartDataPoint[]
  left?: number
  right?: number
  refAreaLeft?: number
  refAreaRight?: number
  handleMouseDown: CategoricalChartFunc
  handleMouseMove: CategoricalChartFunc
  zoom: () => void
}

export const RegionCostsChartTab = ({
  selectedService,
  selectedCostType,
  processedData,
  zoomData,
  left,
  right,
  refAreaLeft,
  refAreaRight,
  handleMouseDown,
  handleMouseMove,
  zoom,
}: RegionCostsChartTabProps) => {
  return (
    <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border min-w-0 max-w-full">
      <div className="p-6 rounded-lg">
        {processedData.chartData.length > 0 && processedData.chartOptions.length > 0 ? (
          <div className="space-y-6">
            {/* Service Total Display */}
            {selectedService && (
              <div className="flex items-center justify-center mb-4">
                <div className="flex items-center gap-4">
                  <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                    Service Total for {selectedService} ({formatCostTypeLabel(selectedCostType)}):
                  </span>
                  <span className="text-lg font-bold text-green-600 dark:text-green-400">
                    $
                    {((processedData.serviceTotals as Record<string, number>)[selectedService] || 0).toFixed(2)}
                  </span>
                </div>
              </div>
            )}

            {/* Stacked Area Chart for Usage Types */}
            {selectedService && (
              <DateRangeChart
                data={processedData.chartData}
                chartType="area"
                height={400}
                customTooltip={(props) => <CostChartTooltip {...props} />}
                zoomData={zoomData}
                left={typeof left === 'number' ? left : undefined}
                right={typeof right === 'number' ? right : undefined}
                refAreaLeft={typeof refAreaLeft === 'number' ? refAreaLeft : undefined}
                refAreaRight={typeof refAreaRight === 'number' ? refAreaRight : undefined}
                onMouseDown={handleMouseDown}
                onMouseMove={handleMouseMove}
                onMouseUp={zoom}
              >
                {/* Generate an Area for each usage type in the selected service */}
                {processedData
                  .getUsageTypesForService(selectedService)
                  .map((usageType, index) => {
                    const usageKey = `${selectedService}:${usageType}`
                    const color = getChartColor(index)

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
  )
}


