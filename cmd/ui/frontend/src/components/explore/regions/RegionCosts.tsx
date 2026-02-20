import { useState, useEffect } from 'react'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/common/ui/select'
import { generateCostsFilename } from '@/lib/utils'
import { useRegionCostFilters, useSessionId } from '@/stores/store'
import { useChartZoom } from '@/hooks/useChartZoom'
import { useRegionCostsData } from '@/hooks/useRegionCostsData'
import { useDateFilters } from '@/hooks/useDateFilters'
import { useDownloadHandlers } from '@/hooks/useDownloadHandlers'
import { DateRangePicker } from '@/components/common/DateRangePicker'
import { ErrorDisplay } from '@/components/common/ErrorDisplay'
import { DataViewTabs } from '@/components/common/DataViewTabs'
import { RegionCostsChartTab } from './RegionCostsChartTab'
import { RegionCostsTableTab } from './RegionCostsTableTab'
import { apiClient } from '@/services/apiClient'
import type { CostsApiResponse } from '@/types/api'
import { COST_TYPES, AWS_SERVICES } from '@/constants'
import type { CostType } from '@/types'

interface RegionCostsProps {
  region: {
    name: string
  }
  isActive?: boolean
}

export const RegionCosts = ({ region, isActive }: RegionCostsProps) => {
  const sessionId = useSessionId()
  const [isLoading, setIsLoading] = useState(false)
  const [costsResponse, setCostsResponse] = useState<CostsApiResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selectedService, setSelectedService] = useState<string>('')
  const [selectedTableService, setSelectedTableService] = useState<string>('')
  const [selectedCostType, setSelectedCostType] = useState<CostType>(COST_TYPES.UNBLENDED_COST)
  const [serviceDefaultSet, setServiceDefaultSet] = useState(false)

  // Region-specific state from Zustand
  const { startDate, endDate, activeCostsTab, setStartDate, setEndDate, setActiveCostsTab } =
    useRegionCostFilters(region.name)

  // Reset selected services and flags when region changes
  useEffect(() => {
    setSelectedService('')
    setSelectedTableService('')
    setServiceDefaultSet(false)
  }, [region.name])

  // Process costs data for table, CSV, and chart formats using backend aggregates
  const processedData = useRegionCostsData(costsResponse, selectedTableService, selectedCostType)

  // Initialize zoom functionality
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
    initialData: processedData.chartData,
    dataKey: 'epochTime',
    isNumericAxis: true,
    onDateRangeChange: (startDate, endDate) => {
      setStartDate(startDate)
      setEndDate(endDate)
    },
  })

  // Use date filters hook with metadata for auto-initialization and reset functions
  const { resetToMetadataDates, resetStartDateToMetadata, resetEndDateToMetadata } = useDateFilters(
    {
      startDate,
      endDate,
      setStartDate,
      setEndDate,
      metadata: costsResponse?.metadata,
      onReset: resetZoom,
      autoSetDefaults: true,
    }
  )

  // Update zoom data when processedData changes
  useEffect(() => {
    updateData(processedData.chartData)
  }, [processedData.chartData, updateData])

  // Set Amazon MSK as default chart service when data loads
  useEffect(() => {
    if (serviceDefaultSet || processedData.chartOptions.length === 0) return

    // Try to find Amazon MSK in the chart options
    const mskOption = processedData.chartOptions.find((option) => option.value === AWS_SERVICES.MSK)

    // Default to MSK if available, otherwise use first option
    if (mskOption) {
      setSelectedService(mskOption.value)
      setServiceDefaultSet(true)
    } else if (processedData.chartOptions.length > 0) {
      setSelectedService(processedData.chartOptions[0].value)
      setServiceDefaultSet(true)
    }
  }, [processedData.chartOptions, serviceDefaultSet])

  // Set first service as default for table when data loads
  useEffect(() => {
    if (processedData.services.length > 0 && !selectedTableService) {
      setSelectedTableService(processedData.services[0])
    }
  }, [processedData.services, selectedTableService])

  // Download handlers
  const { handleDownloadCSV, handleDownloadJSON } = useDownloadHandlers({
    csvData: processedData.csvData,
    jsonData: costsResponse,
    filename: generateCostsFilename(region.name),
  })

  // Fetch costs when component becomes active or dates change
  useEffect(() => {
    if (!isActive || !region.name) {
      setIsLoading(false)
      return
    }

    const fetchCosts = async () => {
      setIsLoading(true)
      setError(null)

      try {
        const data = await apiClient.costs.getCosts(region.name, sessionId, {
          startDate,
          endDate,
        })

        setCostsResponse(data)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch costs')
      } finally {
        setIsLoading(false)
      }
    }

    fetchCosts()
  }, [isActive, region.name, startDate, endDate, selectedCostType, sessionId])

  // Show error state
  if (error) {
    return (
      <ErrorDisplay
        title="Region Costs"
        error={error}
        context="costs"
      />
    )
  }

  // Main component render
  return (
    <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-6 transition-colors">
      {/* Filters: Cost Type, Service, and Date Picker Controls */}
      <div className="flex flex-col gap-4 mb-6">
        {/* Top row: Service and Cost Type Selectors */}
        <div className="flex flex-col sm:flex-row gap-4">
          {/* Service Selector */}
          <div className="flex flex-col space-y-2">
            <label className="text-sm font-medium text-gray-700 dark:text-gray-300">Service</label>
            <Select
              value={selectedService}
              onValueChange={setSelectedService}
            >
              <SelectTrigger className="w-[300px]">
                <SelectValue placeholder="Select service for chart" />
              </SelectTrigger>
              <SelectContent>
                {processedData.chartOptions.map((option) => (
                  <SelectItem
                    key={option.value}
                    value={option.value}
                  >
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Cost Type Selector */}
          <div className="flex flex-col space-y-2">
            <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Cost Type
            </label>
            <Select
              value={selectedCostType}
              onValueChange={(value) => setSelectedCostType(value as CostType)}
            >
              <SelectTrigger className="w-[300px]">
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
        />
      </div>

      {/* Results Section */}
      {costsResponse && (
        <DataViewTabs
          activeTab={activeCostsTab}
          onTabChange={(id) => setActiveCostsTab(id)}
          onDownloadJSON={handleDownloadJSON}
          onDownloadCSV={handleDownloadCSV}
          jsonData={costsResponse}
          csvData={processedData.csvData}
          renderChart={() => (
            <RegionCostsChartTab
              selectedService={selectedService}
              selectedCostType={selectedCostType}
              processedData={processedData}
              zoomData={zoomData}
              left={typeof left === 'number' ? left : undefined}
              right={typeof right === 'number' ? right : undefined}
              refAreaLeft={typeof refAreaLeft === 'number' ? refAreaLeft : undefined}
              refAreaRight={typeof refAreaRight === 'number' ? refAreaRight : undefined}
              handleMouseDown={handleMouseDown}
              handleMouseMove={handleMouseMove}
              zoom={zoom}
            />
          )}
          renderTable={() => (
            <RegionCostsTableTab
              processedData={processedData}
              selectedCostType={selectedCostType}
              selectedTableService={selectedTableService}
              setSelectedTableService={setSelectedTableService}
            />
          )}
        />
      )}

      {!costsResponse && !error && !isLoading && (
        <div className="text-center py-8">
          <p className="text-gray-500 dark:text-gray-400">
            Select dates and fetch costs to view data for this region.
          </p>
        </div>
      )}
    </div>
  )
}
