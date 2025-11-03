import { useState, useCallback } from 'react'
import type { ChartDataPoint } from '@/components/common/DateRangeChart'

/**
 * Internal state for chart zoom functionality
 */
interface ZoomState {
  data: ChartDataPoint[]
  left: string | number
  right: string | number
  refAreaLeft: string | number
  refAreaRight: string | number
  top: string | number
  bottom: string | number
  animation: boolean
}

/**
 * Configuration options for the useChartZoom hook
 */
interface UseChartZoomProps {
  /** Chart data points to enable zooming on */
  initialData: ChartDataPoint[]
  /** Key for the X-axis data (default: 'formattedDate') */
  dataKey?: string
  /** Key for the Y-axis data to calculate zoom domain */
  yAxisKey?: string
  /** Offset to add to Y-axis min/max (default: 1) */
  yAxisOffset?: number
  /** Whether X-axis uses numeric values (e.g., epoch time) instead of strings */
  isNumericAxis?: boolean
  /** Callback invoked when zoom selection changes (for numeric axis only) */
  onDateRangeChange?: (startDate: Date, endDate: Date) => void
}

/**
 * Custom hook to manage interactive chart zooming with mouse selection.
 * Provides drag-to-zoom functionality for Recharts with support for both
 * numeric (epoch time) and string-based (formatted dates) X-axis values.
 *
 * Features:
 * - Drag to select zoom area (mouseDown → mouseMove → mouseUp)
 * - Automatic Y-axis domain calculation for zoomed region
 * - Support for both numeric and string-based X-axis
 * - Reset/zoom out functionality
 * - Optional callback on zoom selection (for date range changes)
 *
 * @param {UseChartZoomProps} config - Configuration options
 * @returns {Object} Zoom state and control functions
 */
export const useChartZoom = ({
  initialData,
  dataKey = 'formattedDate',
  yAxisKey,
  yAxisOffset = 1,
  isNumericAxis = false,
  onDateRangeChange,
}: UseChartZoomProps) => {
  const initialState: ZoomState = {
    data: initialData,
    left: 'dataMin',
    right: 'dataMax',
    refAreaLeft: '',
    refAreaRight: '',
    top: 'dataMax+1',
    bottom: 'dataMin-1',
    animation: true,
  }

  const [state, setState] = useState<ZoomState>(initialState)

  // Helper function to get Y-axis domain for numeric data
  const getAxisYDomain = useCallback(
    (from: number, to: number, ref: string, offset: number) => {
      if (!initialData || initialData.length === 0) return [0, 100]

      const refData = initialData.slice(from - 1, to)
      if (refData.length === 0) return [0, 100]

      const firstValue = refData[0][ref]
      if (firstValue === null || firstValue === undefined || typeof firstValue !== 'number') {
        return [0, 100]
      }

      let bottom: number = firstValue
      let top: number = firstValue

      refData.forEach((d) => {
        const value = d[ref]
        if (value !== null && value !== undefined && typeof value === 'number') {
          if (value > top) top = value
          if (value < bottom) bottom = value
        }
      })

      return [Math.floor(bottom) - offset, Math.floor(top) + offset]
    },
    [initialData]
  )

  // Recharts mouse event with activeLabel property
  interface RechartsMouseEvent {
    activeLabel?: string | number
    [key: string]: unknown
  }

  // Handle mouse down event
  const handleMouseDown = useCallback((e: RechartsMouseEvent) => {
    if (e && e.activeLabel !== undefined) {
      setState((prevState) => ({
        ...prevState,
        refAreaLeft: e.activeLabel as string | number,
      }))
    }
  }, [])

  // Handle mouse move event
  const handleMouseMove = useCallback((e: RechartsMouseEvent) => {
    setState((prevState) => {
      if (prevState.refAreaLeft && e && e.activeLabel !== undefined) {
        return {
          ...prevState,
          refAreaRight: e.activeLabel,
        }
      }
      return prevState
    })
  }, [])

  // Zoom function
  const zoom = useCallback(() => {
    setState((prevState) => {
      let { refAreaLeft, refAreaRight } = prevState
      const { data } = prevState

      if (refAreaLeft === refAreaRight || refAreaRight === '') {
        return {
          ...prevState,
          refAreaLeft: '',
          refAreaRight: '',
        }
      }

      // Ensure left is less than right
      if (refAreaLeft > refAreaRight) {
        ;[refAreaLeft, refAreaRight] = [refAreaRight, refAreaLeft]
      }

      let leftIndex = 0
      let rightIndex = data.length - 1
      let leftValue: string | number = refAreaLeft
      let rightValue: string | number = refAreaRight

      if (isNumericAxis && typeof refAreaLeft === 'number' && typeof refAreaRight === 'number') {
        // For numeric axis (like epoch time), use the values directly
        leftValue = refAreaLeft
        rightValue = refAreaRight

        // Find indices for Y-axis calculation
        const leftItem = data.find((item) => {
          const keyValue = item[dataKey]
          return keyValue !== null && keyValue !== undefined && Number(keyValue) >= refAreaLeft
        })
        const rightItem = data.find((item) => {
          const keyValue = item[dataKey]
          return keyValue !== null && keyValue !== undefined && Number(keyValue) >= refAreaRight
        })
        leftIndex = leftItem ? data.indexOf(leftItem) : 0
        rightIndex = rightItem ? data.indexOf(rightItem) : data.length - 1
      } else {
        // For string-based X-axis (like formatted dates), use the string values directly
        if (typeof refAreaLeft === 'string' && typeof refAreaRight === 'string') {
          leftIndex = data.findIndex((item) => item[dataKey] === refAreaLeft)
          rightIndex = data.findIndex((item) => item[dataKey] === refAreaRight)

          if (leftIndex === -1) leftIndex = 0
          if (rightIndex === -1) rightIndex = data.length - 1
        }
      }

      // Calculate Y-axis domain if yAxisKey is provided
      let bottom: string | number = 'dataMin-1'
      let top: string | number = 'dataMax+1'

      if (yAxisKey && leftIndex >= 0 && rightIndex >= 0) {
        const [calcBottom, calcTop] = getAxisYDomain(
          leftIndex + 1,
          rightIndex + 1,
          yAxisKey,
          yAxisOffset
        )
        bottom = calcBottom
        top = calcTop
      }

      // Call the date range change callback if provided and we're using numeric axis (epoch time)
      if (
        onDateRangeChange &&
        isNumericAxis &&
        typeof leftValue === 'number' &&
        typeof rightValue === 'number'
      ) {
        const startDate = new Date(leftValue)
        const endDate = new Date(rightValue)
        onDateRangeChange(startDate, endDate)
      }

      return {
        ...prevState,
        refAreaLeft: '',
        refAreaRight: '',
        data: data.slice(),
        left: leftValue,
        right: rightValue,
        bottom,
        top,
      }
    })
  }, [dataKey, yAxisKey, yAxisOffset, getAxisYDomain, isNumericAxis, onDateRangeChange])

  // Zoom out function
  const zoomOut = useCallback(() => {
    setState((prevState) => ({
      ...prevState,
      data: initialData.slice(),
      refAreaLeft: '',
      refAreaRight: '',
      left: 'dataMin',
      right: 'dataMax',
      top: 'dataMax+1',
      bottom: 'dataMin-1',
    }))
  }, [initialData])

  // Update data when initialData changes
  const updateData = useCallback((newData: ChartDataPoint[]) => {
    setState((prevState) => ({
      ...prevState,
      data: newData,
    }))
  }, [])

  return {
    ...state,
    handleMouseDown,
    handleMouseMove,
    zoom,
    zoomOut,
    resetZoom: zoomOut, // Alias for external use
    updateData,
    isZoomed: state.left !== 'dataMin' || state.right !== 'dataMax',
  }
}
