import { useMemo } from 'react'
import type { ReactNode } from 'react'
import {
  AreaChart,
  LineChart,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  ReferenceArea,
} from 'recharts'
import { format } from 'date-fns'
import { formatDateShort } from '@/lib/formatters'

interface DateRangeChartProps {
  data: Array<{
    date: string
    formattedDate?: string
    epochTime: number
    [key: string]: string | number | null | undefined
  }>
  children: ReactNode
  chartType?: 'area' | 'line'
  height?: number | string
  yAxisFormatter?: (value: number) => string
  tooltipLabelFormatter?: (label: number | string) => string
  customTooltip?: (props: any) => ReactNode
  margin?: {
    top?: number
    right?: number
    left?: number
    bottom?: number
  }
  // Zoom props from useChartZoom
  zoomData?: any[]
  left?: number
  right?: number
  refAreaLeft?: number
  refAreaRight?: number
  onMouseDown?: (e: any) => void
  onMouseMove?: (e: any) => void
  onMouseUp?: () => void
}

/**
 * DateRangeChart - Wrapper for date-based charts with consistent styling
 * Supports both AreaChart and LineChart through chartType prop
 */
export default function DateRangeChart({
  data,
  children,
  chartType = 'area',
  height = 400,
  yAxisFormatter,
  tooltipLabelFormatter,
  customTooltip,
  margin = { top: 5, right: 30, left: 20, bottom: 5 },
  zoomData,
  left,
  right,
  refAreaLeft,
  refAreaRight,
  onMouseDown,
  onMouseMove,
  onMouseUp,
}: DateRangeChartProps) {
  const chartData = zoomData && zoomData.length > 0 ? zoomData : data

  // Calculate domain from data when left/right are not provided
  // Always use 'data' prop for domain calculation to ensure we have the full dataset
  const xAxisDomain = useMemo(() => {
    if (
      left !== undefined &&
      right !== undefined &&
      typeof left === 'number' &&
      typeof right === 'number'
    ) {
      return [left, right]
    }

    // Calculate min/max from the full data set (not zoomData) for domain
    // This ensures we always have valid numeric values
    if (data && data.length > 0) {
      const epochTimes = data
        .map((item) => item.epochTime)
        .filter((time): time is number => typeof time === 'number')

      if (epochTimes.length > 0) {
        const min = Math.min(...epochTimes)
        const max = Math.max(...epochTimes)
        // Add small padding to ensure data points are fully visible
        const padding = (max - min) * 0.01
        return [min - padding, max + padding]
      }
    }

    // Fallback: return a default numeric range if no data (shouldn't happen, but prevents errors)
    return [0, Date.now()]
  }, [data, left, right])

  // Default tooltip formatter
  const defaultTooltipLabelFormatter = (label: number | string) => {
    if (typeof label === 'number') {
      return format(new Date(label), 'MMM dd, yyyy HH:mm')
    }
    return label ? format(new Date(label), 'MMM dd, yyyy HH:mm') : 'Unknown Date'
  }

  // Default Y-axis formatter (no formatting)
  const defaultYAxisFormatter = (value: number) => String(value)

  const ChartComponent = chartType === 'area' ? AreaChart : LineChart

  return (
    <div style={{ userSelect: 'none' }}>
      <ResponsiveContainer
        width="100%"
        height={height}
      >
        <ChartComponent
          data={chartData}
          margin={margin}
          onMouseDown={onMouseDown}
          onMouseMove={onMouseMove}
          onMouseUp={onMouseUp}
        >
          <CartesianGrid
            strokeDasharray="3 3"
            className="opacity-30"
          />
          <XAxis
            allowDataOverflow
            dataKey="epochTime"
            domain={xAxisDomain}
            type="number"
            scale="time"
            tickFormatter={(value) => formatDateShort(new Date(value).toISOString())}
            tick={{ fontSize: 12, fill: 'currentColor' }}
            className="text-gray-700 dark:text-gray-200"
          />
          <YAxis
            tick={{ fontSize: 12, fill: 'currentColor' }}
            className="text-gray-700 dark:text-gray-200"
            tickFormatter={yAxisFormatter || defaultYAxisFormatter}
          />
          {customTooltip ? (
            <Tooltip content={customTooltip} />
          ) : (
            <Tooltip
              labelFormatter={(label) =>
                tooltipLabelFormatter
                  ? tooltipLabelFormatter(label)
                  : defaultTooltipLabelFormatter(label)
              }
            />
          )}
          {refAreaLeft && refAreaRight && (
            <ReferenceArea
              x1={refAreaLeft}
              x2={refAreaRight}
              strokeOpacity={0.3}
            />
          )}
          {children}
        </ChartComponent>
      </ResponsiveContainer>
    </div>
  )
}

/**
 * Default tooltip for cost charts (shows currency formatting)
 */
export function CostChartTooltip({ active, payload, label }: any) {
  if (active && payload && payload.length > 0) {
    const nonZeroEntries = payload.filter((entry: any) => entry.value && entry.value > 0)

    if (nonZeroEntries.length === 0) return null

    const sortedEntries = nonZeroEntries.sort((a: any, b: any) => (b.value || 0) - (a.value || 0))

    const total = sortedEntries.reduce((sum: number, entry: any) => sum + (entry.value || 0), 0)

    return (
      <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-lg p-3 shadow-lg">
        <p className="text-gray-700 dark:text-gray-200 text-sm font-medium mb-2">
          {label ? format(new Date(label), 'MMM dd, yyyy HH:mm') : 'Unknown Date'}
        </p>
        <div className="space-y-1">
          {sortedEntries.map((entry: any, index: number) => (
            <p
              key={index}
              className="text-gray-900 dark:text-gray-100 text-sm flex items-center justify-between"
            >
              <span
                className="flex items-center"
                style={{ color: entry.color }}
              >
                <span
                  className="inline-block w-3 h-3 rounded-full mr-2"
                  style={{ backgroundColor: entry.color }}
                ></span>
                {entry.name}:
              </span>
              <span className="ml-2 font-mono">${(entry.value || 0).toFixed(2)}</span>
            </p>
          ))}
        </div>
        <div className="border-t border-gray-200 dark:border-gray-600 mt-2 pt-2">
          <p className="text-gray-900 dark:text-gray-100 text-sm font-semibold flex justify-between">
            <span>Total:</span>
            <span className="font-mono">${total.toFixed(2)}</span>
          </p>
        </div>
      </div>
    )
  }
  return null
}

/**
 * Simple tooltip for single-value charts (like metrics)
 */
export function SimpleChartTooltip({ active, payload, label, labelKey }: any) {
  if (active && payload && payload.length) {
    return (
      <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 rounded-lg p-3 shadow-lg">
        <p className="text-gray-700 dark:text-gray-200 text-sm font-medium mb-1">
          {label ? format(new Date(label), 'MMM dd, yyyy HH:mm') : 'Unknown Date'}
        </p>
        <p className="text-gray-900 dark:text-gray-100 text-sm">
          <span className="font-medium">{labelKey || payload[0].name}:</span>{' '}
          {payload[0].value !== null && payload[0].value !== undefined
            ? payload[0].value
            : 'No data'}
        </p>
      </div>
    )
  }
  return null
}
