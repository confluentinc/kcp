import { useRef, useState } from 'react'
import TCOInputsPage from '@/components/tco/TCOInputsPage'
import Sidebar from '@/components/explore/Sidebar'
import MigrationAssetsPage from '@/components/migration/MigrationAssets'
import Explore from '@/components/explore/Explore'
import AppHeader from '@/components/common/AppHeader'
import Tabs from '@/components/common/Tabs'
import { useAppStore } from '@/stores/store'
import { apiClient } from '@/services/apiClient'
import type { StateUploadRequest } from '@/types/api'
import { PageErrorBoundary } from '@/components/common/ErrorBoundary'
import { TOP_LEVEL_TABS } from '@/constants'
import type { TopLevelTab } from '@/types'

export default function Home() {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [activeTopTab, setActiveTopTab] = useState<TopLevelTab>(TOP_LEVEL_TABS.EXPLORE)

  // Global state from Zustand
  const kcpState = useAppStore((state) => state.kcpState)
  const isProcessing = useAppStore((state) => state.isProcessing)
  const error = useAppStore((state) => state.error)
  const setKcpState = useAppStore((state) => state.setKcpState)
  const setIsProcessing = useAppStore((state) => state.setIsProcessing)
  const setError = useAppStore((state) => state.setError)
  const clearSelection = useAppStore((state) => state.clearSelection)
  const selectSummary = useAppStore((state) => state.selectSummary)

  const handleFileUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) return

    setIsProcessing(true)
    setError(null)
    clearSelection()

    const reader = new FileReader()
    reader.onload = async (e) => {
      try {
        const content = e.target?.result as string
        const parsed = JSON.parse(content) as StateUploadRequest

        // Validate that we have a Discovery object with regions
        if (parsed && typeof parsed === 'object' && 'regions' in parsed) {
          // Call the /upload-state endpoint to process the discovery data
          const result = await apiClient.state.uploadState(parsed)

          // Set the entire processed state in one action
          if (result && result.regions) {
            setKcpState(result)
            setIsProcessing(false)

            // Auto-select summary view if we have regions
            if (result.regions.length > 0) {
              selectSummary()
            }
          } else {
            throw new Error('Invalid response format from server')
          }
        } else {
          throw new Error('Invalid file format. Expected a KCP state file with regions.')
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to process file')
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

          {kcpState !== null ? (
            <div className="flex flex-1 flex-col">
              <Tabs
                tabs={[
                  { id: 'explore', label: 'Explore' },
                  { id: 'migration-assets', label: 'Migrate' },
                  { id: 'tco-inputs', label: 'TCO Inputs' },
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
