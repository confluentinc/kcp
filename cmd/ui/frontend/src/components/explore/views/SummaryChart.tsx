import { Area, Legend } from 'recharts'
import type { CategoricalChartFunc } from 'recharts/types/chart/types'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/common/ui/select'
import { DateRangeChart, CostChartTooltip } from '@/components/common/DateRangeChart'
import type { RegionBreakdown, ChartDataPoint } from '@/lib/costAggregationUtils'
import { COST_TYPES } from '@/constants'
import type { CostType } from '@/types'

interface SummaryChartProps {
  chartData: ChartDataPoint[]
  regionBreakdown: RegionBreakdown[]
  selectedCostType: CostType
  onCostTypeChange: (costType: CostType) => void
  zoomData: ChartDataPoint[]
  left?: number
  right?: number
  refAreaLeft?: number
  refAreaRight?: number
  onMouseDown?: CategoricalChartFunc
  onMouseMove?: CategoricalChartFunc
  onMouseUp?: () => void
}

const CHART_COLORS = [
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

export const SummaryChart = ({
  chartData,
  regionBreakdown,
  selectedCostType,
  onCostTypeChange,
  zoomData,
  left,
  right,
  refAreaLeft,
  refAreaRight,
  onMouseDown,
  onMouseMove,
  onMouseUp,
}: SummaryChartProps) => {
  return (
    <div className="w-full">
      <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
        <div className="flex items-center justify-between mb-6">
          <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
            MSK Cost Over Time by Region
          </h3>
          <div className="flex items-center gap-4">
            <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Cost Type:
            </label>
            <Select
              value={selectedCostType}
              onValueChange={(value) => onCostTypeChange(value as CostType)}
            >
              <SelectTrigger className="w-[200px]">
                <SelectValue placeholder="Select cost type" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={COST_TYPES.UNBLENDED_COST}>Unblended Cost</SelectItem>
                <SelectItem value={COST_TYPES.BLENDED_COST}>Blended Cost</SelectItem>
                <SelectItem value={COST_TYPES.AMORTIZED_COST}>Amortized Cost</SelectItem>
                <SelectItem value={COST_TYPES.NET_AMORTIZED_COST}>Net Amortized Cost</SelectItem>
                <SelectItem value={COST_TYPES.NET_UNBLENDED_COST}>Net Unblended Cost</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        {chartData.length > 0 ? (
          <div className="h-96">
            <DateRangeChart
              data={chartData}
              chartType="area"
              height={400}
              yAxisFormatter={(value) => `$${value.toFixed(2)}`}
              customTooltip={CostChartTooltip}
              zoomData={zoomData}
              left={typeof left === 'number' ? left : undefined}
              right={typeof right === 'number' ? right : undefined}
              refAreaLeft={typeof refAreaLeft === 'number' ? refAreaLeft : undefined}
              refAreaRight={typeof refAreaRight === 'number' ? refAreaRight : undefined}
              onMouseDown={onMouseDown}
              onMouseMove={onMouseMove}
              onMouseUp={onMouseUp}
            >
              <Legend />
              {regionBreakdown.map((region, index) => {
                const color = CHART_COLORS[index % CHART_COLORS.length]

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
            </DateRangeChart>
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
