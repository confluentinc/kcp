import { useState, useCallback } from 'react'

interface ZoomState {
  data: any[]
  left: string | number
  right: string | number
  refAreaLeft: string | number
  refAreaRight: string | number
  top: string | number
  bottom: string | number
  animation: boolean
}

interface UseChartZoomProps {
  initialData: any[]
  dataKey?: string
  yAxisKey?: string
  yAxisOffset?: number
  isNumericAxis?: boolean
  onDateRangeChange?: (startDate: Date, endDate: Date) => void
}

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

      let bottom = refData[0][ref] || 0
      let top = refData[0][ref] || 0

      refData.forEach((d) => {
        if (d[ref] > top) top = d[ref]
        if (d[ref] < bottom) bottom = d[ref]
      })

      return [(bottom | 0) - offset, (top | 0) + offset]
    },
    [initialData]
  )

  // Handle mouse down event
  const handleMouseDown = useCallback((e: any) => {
    if (e && e.activeLabel !== undefined) {
      setState((prevState) => ({
        ...prevState,
        refAreaLeft: e.activeLabel,
      }))
    }
  }, [])

  // Handle mouse move event
  const handleMouseMove = useCallback((e: any) => {
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

      if (isNumericAxis) {
        // For numeric axis (like epoch time), use the values directly
        leftValue = refAreaLeft
        rightValue = refAreaRight

        // Find indices for Y-axis calculation
        leftIndex = data.findIndex((item) => item[dataKey] >= refAreaLeft)
        rightIndex = data.findIndex((item) => item[dataKey] >= refAreaRight)

        if (leftIndex === -1) leftIndex = 0
        if (rightIndex === -1) rightIndex = data.length - 1
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
  const updateData = useCallback((newData: any[]) => {
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
