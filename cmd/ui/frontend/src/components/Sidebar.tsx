import { useState, useEffect } from 'react'
import { Button } from '@/components/ui/button'
import type { Cluster, Region } from '@/types'

interface SidebarProps {
  onFileUpload: () => void
  regions: Region[]
  onClusterSelect: (cluster: Cluster, regionName: string) => void
  onRegionSelect: (region: Region) => void
  onSummarySelect: () => void
  selectedCluster: { cluster: Cluster; regionName: string } | null
  selectedRegion: Region | null
  selectedSummary: boolean
  isProcessing?: boolean
  error?: string | null
}

export default function Sidebar({
  onFileUpload,
  regions,
  onClusterSelect,
  onRegionSelect,
  onSummarySelect,
  selectedCluster,
  selectedRegion,
  selectedSummary,
  isProcessing = false,
  error = null,
}: SidebarProps) {
  const [regionCostData, setRegionCostData] = useState<Record<string, number>>({})

  // Fetch cost data for all regions
  useEffect(() => {
    if (!regions || regions.length === 0) return

    const fetchAllRegionCosts = async () => {
      try {
        const costPromises = regions.map(async (region) => {
          const url = `/costs/${encodeURIComponent(region.name)}`
          const response = await fetch(url)

          if (!response.ok) {
            return { regionName: region.name, totalCost: 0 }
          }

          const data = await response.json()
          let totalCost = 0

          // Calculate total cost from API response
          if (data?.results && Array.isArray(data.results)) {
            data.results.forEach((cost: any) => {
              if (
                cost &&
                cost.service === 'Amazon Managed Streaming for Apache Kafka' &&
                cost.value
              ) {
                totalCost += parseFloat(cost.value) || 0
              }
            })
          }

          return { regionName: region.name, totalCost }
        })

        const results = await Promise.all(costPromises)
        const costData: Record<string, number> = {}

        results.forEach(({ regionName, totalCost }) => {
          costData[regionName] = totalCost
        })

        setRegionCostData(costData)
      } catch (err) {
        console.error('Error fetching region costs for sidebar:', err)
      }
    }

    fetchAllRegionCosts()
  }, [regions])

  // Calculate region cost totals from fetched data
  const getRegionCostTotal = (region: Region) => {
    return regionCostData[region.name] || 0
  }

  const formatCurrency = (amount: number) =>
    new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'USD',
      maximumFractionDigits: 0,
    }).format(amount)
  return (
    <aside className="w-80 bg-gray-50 dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700 flex-shrink-0 h-screen flex flex-col transition-colors">
      <div className="p-4 flex flex-col h-full">
        <div className="flex-shrink-0 space-y-3">
          <Button
            onClick={onFileUpload}
            variant="outline"
            className="w-full"
            disabled={isProcessing}
          >
            {isProcessing ? 'Processing...' : 'Upload KCP State File'}
          </Button>

          {error && (
            <div className="p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-md">
              <div className="text-sm text-red-800 dark:text-red-200">
                <strong>Error:</strong> {error}
              </div>
            </div>
          )}
        </div>

        {regions.length > 0 && (
          <div className="flex-1 flex flex-col min-h-0 mt-4">
            <div className="border-t border-gray-200 pt-4 flex-1 flex flex-col min-h-0">
              <div className="flex-1 space-y-3 overflow-y-auto pr-2 border border-gray-200 dark:border-gray-600 rounded-lg p-3 bg-white dark:bg-gray-700 min-h-0 transition-colors">
                {/* Summary Section */}
                <div className="space-y-2">
                  <button
                    onClick={onSummarySelect}
                    className={`w-full text-left flex items-center justify-between p-3 rounded-lg transition-colors ${
                      selectedSummary
                        ? 'bg-blue-100 dark:bg-blue-900 border border-blue-200 dark:border-blue-700'
                        : 'hover:bg-gray-100 dark:hover:bg-gray-600'
                    }`}
                  >
                    <div className="flex items-center space-x-2 min-w-0 flex-1">
                      <div
                        className={`w-2 h-2 rounded-full flex-shrink-0 ${
                          selectedSummary ? 'bg-blue-600' : 'bg-gray-500'
                        }`}
                      ></div>
                      <h4
                        className={`text-sm font-medium whitespace-nowrap ${
                          selectedSummary
                            ? 'text-blue-900 dark:text-blue-100'
                            : 'text-gray-800 dark:text-gray-200'
                        }`}
                      >
                        Summary
                      </h4>
                      <span className="text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">
                        ({regions.reduce((sum, r) => sum + (r.clusters?.length || 0), 0)} clusters)
                      </span>
                    </div>
                  </button>

                  {/* Regions under Summary */}
                  <div className="ml-4 space-y-2">
                    {regions.map((region) => {
                      const isRegionSelected = selectedRegion?.name === region.name
                      const regionCostTotal = getRegionCostTotal(region)

                      return (
                        <div
                          key={region.name}
                          className="space-y-1"
                        >
                          <button
                            onClick={() => onRegionSelect(region)}
                            className={`w-full text-left flex items-center justify-between p-2 rounded-md transition-colors ${
                              isRegionSelected
                                ? 'bg-blue-100 dark:bg-blue-900 border border-blue-200 dark:border-blue-700'
                                : 'hover:bg-gray-100 dark:hover:bg-gray-600'
                            }`}
                          >
                            <div className="flex items-center space-x-2 min-w-0 flex-1">
                              <div
                                className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${
                                  isRegionSelected ? 'bg-blue-500' : 'bg-blue-400'
                                }`}
                              ></div>
                              <h5
                                className={`text-xs font-medium whitespace-nowrap ${
                                  isRegionSelected
                                    ? 'text-blue-900 dark:text-blue-100'
                                    : 'text-gray-700 dark:text-gray-300'
                                }`}
                              >
                                {region.name}
                              </h5>
                              <span className="text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">
                                ({region.clusters?.length || 0})
                              </span>
                            </div>
                            <div className="text-xs font-medium text-green-600 dark:text-green-400 whitespace-nowrap ml-2">
                              {formatCurrency(regionCostTotal)}
                            </div>
                          </button>

                          {/* Clusters under each region */}
                          <div className="ml-4 space-y-1">
                            {(region.clusters || []).map((cluster) => {
                              const isSelected =
                                selectedCluster?.cluster.name === cluster.name &&
                                selectedCluster?.regionName === region.name
                              return (
                                <button
                                  key={cluster.name}
                                  onClick={() => onClusterSelect(cluster, region.name)}
                                  className={`w-full text-left px-2 py-1 text-xs rounded-sm transition-colors ${
                                    isSelected
                                      ? 'bg-blue-100 dark:bg-blue-900 text-blue-900 dark:text-blue-100 border border-blue-200 dark:border-blue-700'
                                      : 'text-gray-600 dark:text-gray-300 hover:text-gray-900 dark:hover:text-gray-100 hover:bg-gray-100 dark:hover:bg-gray-600'
                                  }`}
                                >
                                  <div className="flex items-center space-x-2">
                                    <div
                                      className={`w-1 h-1 rounded-full flex-shrink-0 ${
                                        isSelected ? 'bg-blue-500' : 'bg-gray-400'
                                      }`}
                                    ></div>
                                    <span className="truncate">{cluster.name}</span>
                                  </div>
                                </button>
                              )
                            })}
                          </div>
                        </div>
                      )
                    })}
                  </div>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </aside>
  )
}
