import { useRef, useState, useEffect } from 'react'
import { TCOInputs as TCOInputsPage } from '@/components/tco/TCOInputsPage'
import { Sidebar } from '@/components/explore/Sidebar'
import { MigrationAssets as MigrationAssetsPage } from '@/components/migration/MigrationAssets'
import { Explore } from '@/components/explore/Explore'
import { AppHeader } from '@/components/common/AppHeader'
import { useAppStore, useSessionId } from '@/stores/store'
import { apiClient } from '@/services/apiClient'
import type { StateUploadRequest } from '@/types/api'
import {
  PageErrorBoundary,
  ExploreErrorBoundary,
  MigrationErrorBoundary,
  TCOErrorBoundary,
  WorkbenchErrorBoundary,
} from '@/components/common/ErrorBoundary'
import { Workbench } from '@/components/workbench/Workbench'
import { TOP_LEVEL_TABS } from '@/constants'
import type { TopLevelTab } from '@/types'

export const Home = () => {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [activeTopTab, setActiveTopTab] = useState<TopLevelTab>(TOP_LEVEL_TABS.EXPLORE)
  const [isInitialLoading, setIsInitialLoading] = useState(true)

  // Global state from Zustand
  const sessionId = useSessionId()
  const kcpState = useAppStore((state) => state.kcpState)
  const isProcessing = useAppStore((state) => state.isProcessing)
  const error = useAppStore((state) => state.error)
  const setKcpState = useAppStore((state) => state.setKcpState)
  const setIsProcessing = useAppStore((state) => state.setIsProcessing)
  const setError = useAppStore((state) => state.setError)
  const clearSelection = useAppStore((state) => state.clearSelection)
  const selectSummary = useAppStore((state) => state.selectSummary)
  const selectOSKCluster = useAppStore((state) => state.selectOSKCluster)

  // Check for pre-loaded state on mount
  useEffect(() => {
    const checkPreloadedState = async () => {
      try {
        // Backend falls back to "default" session if session-specific state not found
        const response = await apiClient.state.getState(sessionId)

        if (response && response.sources && response.sources.length > 0) {
          setKcpState(response)

          // Auto-select summary view if we have MSK sources with regions
          const mskSource = response.sources.find((s: any) => s.type === 'msk' && s.msk_data !== undefined)
          if (mskSource?.msk_data?.regions && mskSource.msk_data.regions.length > 0) {
            selectSummary()
          } else {
            // Fallback: auto-select first OSK cluster if no MSK sources
            const oskSource = response.sources.find((s: any) => s.type === 'osk' && s.osk_data !== undefined)
            const firstCluster = oskSource?.osk_data?.clusters?.[0]
            if (firstCluster) {
              selectOSKCluster(firstCluster.id)
            }
          }
        } else if (response) {
          setError('State file contains no sources. Run kcp discover (MSK) or kcp scan clusters (OSK) to populate it, then reload.')
        }
      } catch {
        // No pre-loaded state, user will upload manually
      } finally {
        setIsInitialLoading(false)
      }
    }

    checkPreloadedState()
  }, [sessionId, setKcpState, selectSummary, selectOSKCluster])

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
        let parsed: StateUploadRequest
        try {
          parsed = JSON.parse(content) as StateUploadRequest
        } catch {
          throw new Error('The file could not be read — it contains invalid JSON. Please recreate the state file using kcp discover or kcp scan clusters.')
        }

        // Lightweight check: confirm the file is a JSON object before uploading
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          // Call the /upload-state endpoint to process the discovery data
          const result = await apiClient.state.uploadState(parsed, sessionId)

          // Set the entire processed state in one action
          if (result && result.sources) {
            setKcpState(result)
            setIsProcessing(false)

            // Auto-select summary view if we have MSK sources with regions
            const mskSource = result.sources.find((s) => s.type === 'msk' && s.msk_data !== undefined)
            if (mskSource?.msk_data?.regions && mskSource.msk_data.regions.length > 0) {
              selectSummary()
            } else {
              // Fallback: auto-select first OSK cluster if no MSK sources
              const oskSource = result.sources.find((s) => s.type === 'osk' && s.osk_data !== undefined)
              const firstCluster = oskSource?.osk_data?.clusters?.[0]
              if (firstCluster) {
                selectOSKCluster(firstCluster.id)
              }
            }
          } else {
            throw new Error('Invalid response format from server')
          }
        } else {
          throw new Error('Invalid file format. Expected a KCP state file.')
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
      <div className="h-svh flex flex-col w-full bg-background transition-colors overflow-hidden">
        <AppHeader
          onFileUpload={triggerFileUpload}
          isProcessing={isProcessing}
          error={error}
          tabs={kcpState !== null ? [
            { id: 'explore', label: 'Explore' },
            { id: 'workbench', label: 'Workbench' },
            { id: 'migration-assets', label: 'Migrate' },
            { id: 'tco-inputs', label: 'TCO Inputs' },
          ] : undefined}
          activeTab={activeTopTab}
          onTabChange={(id) => setActiveTopTab(id as TopLevelTab)}
        />

        <div className="flex flex-1 flex-col min-h-0">
          <input
            ref={fileInputRef}
            type="file"
            accept=".json"
            onChange={handleFileUpload}
            className="hidden"
          />

          {kcpState !== null ? (
            <div className="flex flex-1 flex-col min-h-0">

              {activeTopTab === TOP_LEVEL_TABS.EXPLORE && (
                <ExploreErrorBoundary>
                  <div className="flex-1 min-h-0 bg-background">
                    <div className="flex h-full">
                      <div className="w-80 bg-secondary border-r border-border flex-shrink-0">
                        <Sidebar />
                      </div>
                      <main className="flex-1 flex flex-col min-w-0 min-h-0">
                        <Explore />
                      </main>
                    </div>
                  </div>
                </ExploreErrorBoundary>
              )}

              {activeTopTab === TOP_LEVEL_TABS.WORKBENCH && (
                <WorkbenchErrorBoundary>
                  <div className="flex-1 min-h-0 bg-background">
                    <Workbench />
                  </div>
                </WorkbenchErrorBoundary>
              )}

              {activeTopTab === TOP_LEVEL_TABS.TCO_INPUTS && (
                <TCOErrorBoundary>
                  <div className="flex-1 min-h-0 overflow-y-auto bg-background">
                    <TCOInputsPage />
                  </div>
                </TCOErrorBoundary>
              )}

              {activeTopTab === TOP_LEVEL_TABS.MIGRATION_ASSETS && (
                <MigrationErrorBoundary>
                  <div className="flex-1 min-h-0 overflow-y-auto bg-background">
                    <MigrationAssetsPage />
                  </div>
                </MigrationErrorBoundary>
              )}
            </div>
          ) : isInitialLoading || isProcessing ? (
            <div className="flex-1 flex items-center justify-center">
              <div className="text-center max-w-md mx-auto px-6">
                <div className="mx-auto w-10 h-10 mb-6">
                  <svg className="animate-spin w-10 h-10 text-accent" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                </div>
                <h2 className="text-xl font-bold text-foreground mb-2">
                  Loading State File
                </h2>
                <p className="text-muted-foreground">
                  {isProcessing ? 'Processing uploaded state file...' : 'Loading pre-configured state file...'}
                </p>
              </div>
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
