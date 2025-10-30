import { useState, useEffect } from 'react'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Download } from 'lucide-react'
import { downloadCSV, downloadJSON, generateCostsFilename } from '@/lib/utils'
import { useRegionCostFilters } from '@/stores/appStore'
import { useChartZoom } from '@/lib/useChartZoom'
import { useRegionCostsData } from '@/hooks/useRegionCostsData'
import DateRangePicker from '@/components/common/DateRangePicker'
import Tabs from '@/components/common/Tabs'
import RegionCostsChartTab from './RegionCostsChartTab'
import RegionCostsTableTab from './RegionCostsTableTab'
import MetricsCodeViewer from '@/components/explore/clusters/MetricsCodeViewer'

interface RegionCostsProps {
  region: {
    name: string
  }
  isActive?: boolean
}

export default function RegionCosts({ region, isActive }: RegionCostsProps) {
  const [isLoading, setIsLoading] = useState(false)
  const [costsResponse, setCostsResponse] = useState<any>(null)
  const [error, setError] = useState<string | null>(null)
  const [selectedService, setSelectedService] = useState<string>('')
  const [selectedTableService, setSelectedTableService] = useState<string>('')
  const [selectedCostType, setSelectedCostType] = useState<string>('unblended_cost')
  const [defaultsSet, setDefaultsSet] = useState(false)

  // Region-specific state from Zustand
  const { startDate, endDate, activeCostsTab, setStartDate, setEndDate, setActiveCostsTab } =
    useRegionCostFilters(region.name)

  // Set default dates from metadata when data is first loaded
  useEffect(() => {
    if (defaultsSet || !costsResponse?.metadata) return

    const metaStartDate = costsResponse.metadata.start_date
    const metaEndDate = costsResponse.metadata.end_date

    // Only set defaults if both dates are valid and no user selection has been made
    if (
      !startDate &&
      !endDate &&
      metaStartDate &&
      metaEndDate &&
      !isNaN(new Date(metaStartDate).getTime()) &&
      !isNaN(new Date(metaEndDate).getTime())
    ) {
      setStartDate(new Date(metaStartDate))
      setEndDate(new Date(metaEndDate))
      setDefaultsSet(true)
    }
  }, [costsResponse, defaultsSet, startDate, endDate, setStartDate, setEndDate])

  // Custom reset functions that use metadata dates
  const resetToMetadataDates = () => {
    if (costsResponse?.metadata) {
      const metaStartDate = costsResponse.metadata.start_date
      const metaEndDate = costsResponse.metadata.end_date

      if (metaStartDate && metaEndDate) {
        setStartDate(new Date(metaStartDate))
        setEndDate(new Date(metaEndDate))
        resetZoom() // Reset chart zoom when dates are reset
      }
    }
  }

  const resetStartDateToMetadata = () => {
    if (costsResponse?.metadata?.start_date) {
      setStartDate(new Date(costsResponse.metadata.start_date))
      resetZoom() // Reset chart zoom when start date is reset
    }
  }

  const resetEndDateToMetadata = () => {
    if (costsResponse?.metadata?.end_date) {
      setEndDate(new Date(costsResponse.metadata.end_date))
      resetZoom() // Reset chart zoom when end date is reset
    }
  }

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

  // Update zoom data when processedData changes
  useEffect(() => {
    updateData(processedData.chartData)
  }, [processedData.chartData, updateData])

  // Set first service as default when data loads
  useEffect(() => {
    if (processedData.services.length > 0 && !selectedTableService) {
      setSelectedTableService(processedData.services[0])
    }
  }, [processedData.services, selectedTableService])

  const handleDownloadCSV = () => {
    const filename = generateCostsFilename(region.name)
    downloadCSV(processedData.csvData, filename)
  }

  const handleDownloadJSON = () => {
    const filename = generateCostsFilename(region.name)
    downloadJSON(costsResponse, filename)
  }

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
        console.log(`Fetching costs for region: ${region.name}`)

        // Build URL with optional date parameters
        let url = `/costs/${encodeURIComponent(region.name)}`
        const params = new URLSearchParams()

        if (startDate) {
          params.append('startDate', startDate.toISOString())
        }
        if (endDate) {
          params.append('endDate', endDate.toISOString())
        }

        if (params.toString()) {
          url += `?${params.toString()}`
        }

        const response = await fetch(url)

        if (!response.ok) {
          throw new Error(`Failed to fetch costs: ${response.status} ${response.statusText}`)
        }

        const data = await response.json()
        setCostsResponse(data)
      } catch (err) {
        console.error('Error fetching costs:', err)
        setError(err instanceof Error ? err.message : 'Failed to fetch costs')
      } finally {
        setIsLoading(false)
      }
    }

    fetchCosts()
  }, [isActive, region.name, startDate, endDate, selectedCostType])

  // Set default selected service when data loads
  useEffect(() => {
    if (processedData.chartOptions.length > 0 && !selectedService) {
      setSelectedService(processedData.chartOptions[0].value)
    }
  }, [processedData.chartOptions, selectedService])

  // Show error state
  if (error) {
    return (
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          Region Costs
        </h3>
        <div className="text-red-500 dark:text-red-400">
          <p className="font-medium">Error loading costs:</p>
          <p className="text-sm mt-1">{error}</p>
        </div>
      </div>
    )
  }

  // Main component render
  return (
    <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
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
              onValueChange={setSelectedCostType}
            >
              <SelectTrigger className="w-[300px]">
                <SelectValue placeholder="Select cost type" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="unblended_cost">Unblended Cost</SelectItem>
                <SelectItem value="blended_cost">Blended Cost</SelectItem>
                <SelectItem value="amortized_cost">Amortized Cost</SelectItem>
                <SelectItem value="net_amortized_cost">Net Amortized Cost</SelectItem>
                <SelectItem value="net_unblended_cost">Net Unblended Cost</SelectItem>
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
        <div className="w-full max-w-full">
          <div className="flex items-center justify-between mb-4">
            <Tabs
              tabs={[
                { id: 'chart', label: 'Chart' },
                { id: 'table', label: 'Table' },
                { id: 'json', label: 'JSON' },
                { id: 'csv', label: 'CSV' },
              ]}
              activeId={activeCostsTab}
              onChange={(id) => setActiveCostsTab(id as any)}
            />
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={handleDownloadJSON}
                className="flex items-center gap-2"
              >
                <Download className="h-4 w-4" />
                JSON
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={handleDownloadCSV}
                className="flex items-center gap-2"
              >
                <Download className="h-4 w-4" />
                CSV
              </Button>
            </div>
          </div>

          {/* Tab Content */}
          <div className="space-y-4 min-w-0">
            {/* Chart Tab */}
            {activeCostsTab === 'chart' && (
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

            {/* Table Tab */}
            {activeCostsTab === 'table' && (
              <RegionCostsTableTab
                processedData={processedData}
                selectedCostType={selectedCostType}
                selectedTableService={selectedTableService}
                setSelectedTableService={setSelectedTableService}
              />
            )}

            {/* JSON Tab */}
            {activeCostsTab === 'json' && (
              <MetricsCodeViewer
                data={JSON.stringify(costsResponse, null, 2)}
                label="JSON"
                onCopy={() => navigator.clipboard.writeText(JSON.stringify(costsResponse, null, 2))}
                isJSON={true}
              />
            )}

            {/* CSV Tab */}
            {activeCostsTab === 'csv' && (
              <MetricsCodeViewer
                data={processedData.csvData}
                label="CSV"
                onCopy={() => navigator.clipboard.writeText(processedData.csvData)}
                isJSON={false}
              />
            )}
          </div>
        </div>
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
