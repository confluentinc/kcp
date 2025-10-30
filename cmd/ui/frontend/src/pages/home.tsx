import { useRef, useState } from 'react'
import TCOInputsPage from '@/components/tco/TCOInputsPage'
import Sidebar from '@/components/explore/Sidebar'
import MigrationAssetsPage from '@/components/migration/MigrationAssetsPage'
import Explore from '@/components/explore/Explore'
import AppHeader from '@/components/common/AppHeader'
import Tabs from '@/components/common/Tabs'
import { useAppStore } from '@/stores/appStore'
import { apiClient } from '@/services/apiClient'
import type { StateUploadRequest } from '@/types/api'
import { PageErrorBoundary } from '@/components/ErrorBoundary'
import { TOP_LEVEL_TABS } from '@/constants'
import type { TopLevelTab } from '@/types'

export default function Home() {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [activeTopTab, setActiveTopTab] = useState<TopLevelTab>(TOP_LEVEL_TABS.EXPLORE)

  // Global state from Zustand
  const {
    regions,
    isProcessing,
    error,
    setRegions,
    setSchemaRegistries,
    setSelectedSummary,
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
        const parsed = JSON.parse(content) as StateUploadRequest

        // Validate that we have a Discovery object with regions
        if (parsed && typeof parsed === 'object' && 'regions' in parsed) {
          // Call the /upload-state endpoint to process the discovery data
          const result = await apiClient.state.uploadState(parsed)

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

  return (
    <PageErrorBoundary>
      <div className="min-h-svh flex flex-col w-full h-full bg-gray-50 dark:bg-card transition-colors">
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
            <div className="flex flex-1 flex-col">
              <Tabs
                tabs={[
                  { id: 'explore', label: 'Explore Costs & Metrics' },
                  { id: 'tco-inputs', label: 'Generate TCO Inputs' },
                  { id: 'migration-assets', label: 'Generate Migration Assets' },
                ]}
                activeId={activeTopTab}
                onChange={(id) => setActiveTopTab(id as TopLevelTab)}
                size="lg"
              />

              {activeTopTab === TOP_LEVEL_TABS.EXPLORE && (
                <div className="flex-1 overflow-hidden bg-white dark:bg-background">
                  <div className="flex h-full">
                    <div className="w-80 bg-gray-50 dark:bg-card border-r border-gray-200 dark:border-border flex-shrink-0">
                      <Sidebar />
                    </div>
                    <main className="flex flex-1 p-4 w-full min-w-0 max-w-full overflow-hidden">
                      <Explore />
                    </main>
                  </div>
                </div>
              )}

              {activeTopTab === TOP_LEVEL_TABS.TCO_INPUTS && (
                <div className="flex-1 overflow-hidden bg-white dark:bg-background">
                  <div className="h-full overflow-auto">
                    <TCOInputsPage />
                  </div>
                </div>
              )}

              {activeTopTab === TOP_LEVEL_TABS.MIGRATION_ASSETS && (
                <div className="flex-1 overflow-hidden bg-white dark:bg-background">
                  <div className="h-full overflow-auto">
                    <MigrationAssetsPage />
                  </div>
                </div>
              )}
            </div>
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
                  Upload a KCP state file to get started with exploring your Kafka clusters,
                  analyzing TCO inputs, and managing migration assets.
                </p>
                <div className="bg-blue-50 dark:bg-accent/20 border border-blue-200 dark:border-border rounded-lg p-4">
                  <p className="text-sm text-blue-800 dark:text-accent">
                    <strong>Getting Started:</strong> Click the "Upload KCP State File" button in
                    the header to begin.
                  </p>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    </PageErrorBoundary>
  )
}
