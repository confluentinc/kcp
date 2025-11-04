import { useMemo, useEffect, useState } from 'react'
import { useRegions, useSummaryDateFilters } from '@/stores/store'
import { useChartZoom } from '@/hooks/useChartZoom'
import { useDateFilters } from '@/hooks/useDateFilters'
import { useSummaryCostsFetch } from '@/hooks/useSummaryCostsFetch'
import { createCostSummary } from '@/lib/costAggregationUtils'
import { DateRangePicker } from '@/components/common/DateRangePicker'
import { SummaryHeader } from './SummaryHeader'
import { SummaryRegionTable } from './SummaryRegionTable'
import { SummaryChart } from './SummaryChart'
import { COST_TYPES } from '@/constants'
import type { CostType } from '@/types'

export const Summary = () => {
  const regions = useRegions()
  const { startDate, endDate, setStartDate, setEndDate } = useSummaryDateFilters()
  const [selectedChartCostType, setSelectedChartCostType] = useState<CostType>(
    COST_TYPES.UNBLENDED_COST
  )

  // Fetch cost data for all regions
  const { regionCostData, error } = useSummaryCostsFetch({
    regions,
    startDate,
    endDate,
  })

  // Process cost data into summary format
  const costSummary = useMemo(
    () => createCostSummary(regionCostData, selectedChartCostType),
    [regionCostData, selectedChartCostType]
  )

  // Initialize zoom functionality (must be before useDateFilters)
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
    updateData,
  } = useChartZoom({
    initialData: costSummary.chartData,
    dataKey: 'epochTime',
    isNumericAxis: true,
    onDateRangeChange: (startDate, endDate) => {
      setStartDate(startDate)
      setEndDate(endDate)
    },
  })

  // Extract metadata from any region's response for date filters
  const metadata = useMemo(() => {
    for (const costResponse of Object.values(regionCostData)) {
      if (costResponse?.metadata) {
        return costResponse.metadata
      }
    }
    return null
  }, [regionCostData])

  // Use date filters hook with metadata
  const { resetToMetadataDates, resetStartDateToMetadata, resetEndDateToMetadata } = useDateFilters(
    {
      startDate,
      endDate,
      setStartDate,
      setEndDate,
      metadata,
      onReset: resetZoom,
      autoSetDefaults: true,
    }
  )

  // Update zoom data when costSummary changes
  useEffect(() => {
    updateData(costSummary.chartData)
  }, [costSummary.chartData, updateData])

  const handlePrint = () => {
    window.print()
  }

  // Show error state
  if (error) {
    return (
      <div className="p-6 space-y-8">
        <div className="text-center">
          <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100 mb-2">
            Cost Analysis Summary
          </h1>
          <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-border rounded-lg p-4 max-w-2xl mx-auto">
            <p className="text-red-800 dark:text-red-200">
              <strong>Error:</strong> {error}
            </p>
          </div>
        </div>
      </div>
    )
  }

  // Show empty state
  if (regions.length === 0) {
    return (
      <div className="p-6 space-y-8">
        <div className="text-center">
          <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100 mb-2">
            Cost Analysis Summary
          </h1>
          <p className="text-lg text-gray-600 dark:text-gray-400">
            Upload a KCP state file to view cost analysis
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-8 print:block">
      <SummaryHeader onPrint={handlePrint} />

      {/* Date Picker Controls */}
      <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
        <DateRangePicker
          startDate={startDate}
          endDate={endDate}
          onStartDateChange={setStartDate}
          onEndDateChange={setEndDate}
          onResetStartDate={resetStartDateToMetadata}
          onResetEndDate={resetEndDateToMetadata}
          onResetBoth={resetToMetadataDates}
          showResetButtons={true}
          showResetBothButton={true}
        />
      </div>

      <SummaryRegionTable regionBreakdown={costSummary.regionBreakdown} />

      <SummaryChart
        chartData={costSummary.chartData}
        regionBreakdown={costSummary.regionBreakdown}
        selectedCostType={selectedChartCostType}
        onCostTypeChange={setSelectedChartCostType}
        zoomData={zoomData}
        left={typeof left === 'number' ? left : undefined}
        right={typeof right === 'number' ? right : undefined}
        refAreaLeft={typeof refAreaLeft === 'number' ? refAreaLeft : undefined}
        refAreaRight={typeof refAreaRight === 'number' ? refAreaRight : undefined}
        onMouseDown={handleMouseDown}
        onMouseMove={handleMouseMove}
        onMouseUp={zoom}
      />
    </div>
  )
}
