import { useRef } from 'react'
import type { Region, Cluster } from '@/types'
import ClusterReport from '@/components/ClusterReport'
import RegionReport from '@/components/RegionReport'
import Summary from '@/components/Summary'
import TCOInputs from '@/components/TCOInputs'
import Explore from '@/components/Explore'
import MigrationAssets from '@/components/MigrationAssets'
import SchemaRegistries from '@/components/SchemaRegistries'
import AppHeader from '@/components/AppHeader'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAppStore } from '@/stores/appStore'

export default function Home() {
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Global state from Zustand - using single selector to avoid multiple subscriptions
  const {
    regions,
    schemaRegistries,
    selectedCluster,
    selectedRegion,
    selectedSummary,
    selectedSchemaRegistries,
    isProcessing,
    error,
    setRegions,
    setSchemaRegistries,
    setSelectedCluster,
    setSelectedRegion,
    setSelectedSummary,
    setSelectedSchemaRegistries,
    setIsProcessing,
    setError,
  } = useAppStore()

  const handleFileUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) return

    // Reset state
    setRegions([])
    setSchemaRegistries([])
    useAppStore.getState().clearSelection()
    setError(null)
    setIsProcessing(true)

    const reader = new FileReader()
    reader.onload = async (e) => {
      try {
        const content = e.target?.result as string
        const parsed = JSON.parse(content)

        console.log(parsed)
        // Validate that we have a Discovery object with regions
        if (parsed && typeof parsed === 'object' && 'regions' in parsed) {
          // Call the /upload-state endpoint to process the discovery data
          const response = await fetch('/upload-state', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify(parsed),
          })

          if (!response.ok) {
            const errorData = await response.json().catch(() => ({}))
            throw new Error(errorData.message || `Server error: ${response.status}`)
          }

          const result = await response.json()
          // Extract the processed regions from the API response
          if (result && result.regions) {
            const processedRegions = result.regions
            setRegions(processedRegions)

            // Process schema registries if available
            if (result.schema_registries) {
              setSchemaRegistries(result.schema_registries)
            }

            // Auto-select Summary if regions are available
            if (processedRegions.length > 0) {
              setSelectedSummary()
            }
          } else {
            throw new Error('Invalid response format from server')
          }
        } else {
          throw new Error('Invalid file format. Expected a KCP state file with regions.')
        }
      } catch (err) {
        console.error('Error processing file:', err)
        setError(err instanceof Error ? err.message : 'Failed to process file')
        setRegions([])
        setSchemaRegistries([])
        useAppStore.getState().clearSelection()
      } finally {
        setIsProcessing(false)
      }
    }
    reader.readAsText(file)
  }

  const triggerFileUpload = () => {
    if (fileInputRef.current) {
      fileInputRef.current.value = ''
    }
    fileInputRef.current?.click()
  }

  const handleClusterSelect = (cluster: Cluster, regionName: string) => {
    setSelectedCluster(cluster, regionName)
  }

  const handleRegionSelect = (region: Region) => {
    setSelectedRegion(region)
  }

  const handleSummarySelect = () => {
    setSelectedSummary()
  }

  const handleSchemaRegistriesSelect = () => {
    setSelectedSchemaRegistries()
  }

  return (
    <div className="min-h-svh flex flex-col w-full h-full bg-gray-50 dark:bg-gray-900 transition-colors">
      <AppHeader
        onFileUpload={triggerFileUpload}
        isProcessing={isProcessing}
        error={error}
      />

      <div className="flex flex-1 flex-col">
        <input
          ref={fileInputRef}
          type="file"
          accept=".json"
          onChange={handleFileUpload}
          className="hidden"
        />

        {regions.length > 0 ? (
          <Tabs
            defaultValue="explore"
            className="flex flex-1 flex-col"
          >
            <div className="bg-white dark:bg-gray-800 border-b-2 border-gray-200 dark:border-gray-700">
              <div className="px-6 pt-6 pb-0">
                <TabsList className="w-full">
                  <TabsTrigger value="explore">Explore Costs & Metrics</TabsTrigger>
                  <TabsTrigger value="tco-inputs">Generate TCO Inputs</TabsTrigger>
                  <TabsTrigger value="migration-assets">Generate Migration Assets</TabsTrigger>
                </TabsList>
              </div>
            </div>

            <TabsContent
              value="explore"
              className="flex-1 overflow-hidden bg-white dark:bg-gray-800"
            >
              <div className="flex h-full">
                <div className="w-80 bg-gray-50 dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700 flex-shrink-0">
                  <Explore
                    regions={regions}
                    onClusterSelect={handleClusterSelect}
                    onRegionSelect={handleRegionSelect}
                    onSummarySelect={handleSummarySelect}
                    selectedCluster={selectedCluster}
                    selectedRegion={selectedRegion}
                    selectedSummary={selectedSummary}
                    selectedSchemaRegistries={selectedSchemaRegistries}
                    onSchemaRegistriesSelect={handleSchemaRegistriesSelect}
                  />
                </div>
                <main className="flex flex-1 p-4 w-full min-w-0 max-w-full overflow-hidden">
                  <div className="mx-auto space-y-6 w-full min-w-0 max-w-full">
                    {selectedSummary && (
                      <Summary />
                    )}
                    {selectedCluster && (
                      <ClusterReport
                        cluster={selectedCluster.cluster}
                        regionName={selectedCluster.regionName}
                        regionData={
                          regions.find((r) => r.name === selectedCluster.regionName) as any
                        }
                      />
                    )}
                    {selectedRegion && (
                      <RegionReport region={selectedRegion} />
                    )}
                    {selectedSchemaRegistries && (
                      <SchemaRegistries schemaRegistries={schemaRegistries} />
                    )}
                  </div>
                </main>
              </div>
            </TabsContent>

            <TabsContent
              value="tco-inputs"
              className="flex-1 overflow-hidden bg-white dark:bg-gray-800"
            >
              <div className="h-full overflow-auto">
                <TCOInputs />
              </div>
            </TabsContent>

            <TabsContent
              value="migration-assets"
              className="flex-1 overflow-hidden bg-white dark:bg-gray-800"
            >
              <div className="h-full overflow-auto">
                <MigrationAssets />
              </div>
            </TabsContent>
          </Tabs>
        ) : (
          <div className="flex-1 flex items-center justify-center">
            <div className="text-center max-w-md mx-auto px-6">
              <div className="mx-auto w-16 h-16 bg-gray-100 dark:bg-gray-700 rounded-full flex items-center justify-center mb-6">
                <span className="text-3xl">📁</span>
              </div>
              <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100 mb-4">
                Welcome to KCP
              </h2>
              <p className="text-gray-600 dark:text-gray-400 mb-6">
                Upload a KCP state file to get started with exploring your Kafka clusters, analyzing
                TCO inputs, and managing migration assets.
              </p>
              <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-lg p-4">
                <p className="text-sm text-blue-800 dark:text-blue-200">
                  <strong>Getting Started:</strong> Click the "Upload KCP State File" button in the
                  header to begin.
                </p>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
