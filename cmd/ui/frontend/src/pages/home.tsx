import { useRef } from 'react'
import Sidebar from '@/components/Sidebar'
import type { Region, Cluster } from '@/types'
import ClusterReport from '@/components/ClusterReport'
import RegionReport from '@/components/RegionReport'
import Summary from '@/components/Summary'
import TCOInputs from '@/components/TCOInputs'
import AppHeader from '@/components/AppHeader'
import { useAppStore } from '@/stores/appStore'

export default function Home() {
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Global state from Zustand - using single selector to avoid multiple subscriptions
  const {
    regions,
    selectedCluster,
    selectedRegion,
    selectedSummary,
    selectedTCOInputs,
    isProcessing,
    error,
    setRegions,
    setSelectedCluster,
    setSelectedRegion,
    setSelectedSummary,
    setSelectedTCOInputs,
    setIsProcessing,
    setError,
  } = useAppStore()

  const handleFileUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) return

    // Reset state
    setRegions([])
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

  const handleTCOInputsSelect = () => {
    setSelectedTCOInputs()
  }

  return (
    <div className="min-h-svh flex flex-col w-full h-full bg-gray-50 dark:bg-gray-900 transition-colors">
      <AppHeader />

      <div className="flex flex-1">
        <Sidebar
          onFileUpload={triggerFileUpload}
          regions={regions}
          onClusterSelect={handleClusterSelect}
          onRegionSelect={handleRegionSelect}
          onSummarySelect={handleSummarySelect}
          onTCOInputsSelect={handleTCOInputsSelect}
          selectedCluster={selectedCluster}
          selectedRegion={selectedRegion}
          selectedSummary={selectedSummary}
          selectedTCOInputs={selectedTCOInputs}
          isProcessing={isProcessing}
          error={error}
        />

        <main className="flex flex-1 p-4 w-full min-w-0 max-w-full overflow-hidden">
          <input
            ref={fileInputRef}
            type="file"
            accept=".json"
            onChange={handleFileUpload}
            className="hidden"
          />

          <div className="mx-auto space-y-6 w-full min-w-0 max-w-full">
            {selectedSummary ? (
              <Summary />
            ) : selectedTCOInputs ? (
              <TCOInputs />
            ) : selectedCluster ? (
              <ClusterReport
                cluster={selectedCluster.cluster}
                regionName={selectedCluster.regionName}
                regionData={regions.find((r) => r.name === selectedCluster.regionName) as any}
              />
            ) : selectedRegion ? (
              <RegionReport region={selectedRegion} />
            ) : null}
          </div>
        </main>
      </div>
    </div>
  )
}
