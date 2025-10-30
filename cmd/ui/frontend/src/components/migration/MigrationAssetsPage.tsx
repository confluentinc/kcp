import { useState } from 'react'
import { useAppStore } from '@/stores/appStore'
import { Modal } from '@/components/ui/modal'
import { Button } from '@/components/ui/button'
import { TerraformCodeViewer } from './TerraformCodeViewer'
import {
  Wizard,
  targetInfraWizardConfig,
  migrationInfraWizardConfig,
  migrationScriptsWizardConfig,
} from '@/components/wizards'

// Type definitions for File System Access API
declare global {
  interface Window {
    showDirectoryPicker(options?: {
      mode?: 'read' | 'readwrite'
      startIn?: 'desktop' | 'downloads' | 'documents'
    }): Promise<FileSystemDirectoryHandle>
  }

  interface FileSystemDirectoryHandle {
    getFileHandle(name: string, options?: { create?: boolean }): Promise<FileSystemFileHandle>
  }

  interface FileSystemFileHandle {
    createWritable(): Promise<FileSystemWritableFileStream>
  }

  interface FileSystemWritableFileStream {
    write(data: string | BufferSource | Blob): Promise<void>
    close(): Promise<void>
  }
}

export default function MigrationAssets() {
  const regions = useAppStore((state) => state.regions)
  const [isWizardOpen, setIsWizardOpen] = useState(false)
  const [wizardType, setWizardType] = useState<
    'target-infra' | 'migration-infra' | 'migration-scripts' | null
  >(null)
  const [selectedClusterForWizard, setSelectedClusterForWizard] = useState<{
    cluster: any
    regionName: string
  } | null>(null)

  // Track active tab for each cluster (persisted in store)
  const migrationAssetTabs = useAppStore((state) => state.migrationAssetTabs)
  const setMigrationAssetTab = useAppStore((state) => state.setMigrationAssetTab)
  const [activeFileTabs, setActiveFileTabs] = useState<Record<string, string>>({})
  // Track which cluster section is expanded (persisted in store)
  const expandedCluster = useAppStore((state) => state.expandedMigrationCluster)
  const setExpandedCluster = useAppStore((state) => state.setExpandedMigrationCluster)

  // Flatten all clusters from all regions
  const allClusters = regions.flatMap((region) =>
    (region.clusters || [])
      .filter((cluster) => cluster.aws_client_information?.msk_cluster_config?.Provisioned)
      .map((cluster) => ({
        cluster,
        regionName: region.name,
      }))
  )

  const handleCreateTargetInfrastructure = (cluster: any, regionName: string) => {
    setSelectedClusterForWizard({ cluster, regionName })
    setWizardType('target-infra')
    setIsWizardOpen(true)
  }

  const handleCreateMigrationInfrastructure = (cluster: any, regionName: string) => {
    setSelectedClusterForWizard({ cluster, regionName })
    setWizardType('migration-infra')
    setIsWizardOpen(true)
  }

  const handleCreateMigrationScripts = (cluster: any, regionName: string) => {
    setSelectedClusterForWizard({ cluster, regionName })
    setWizardType('migration-scripts')
    setIsWizardOpen(true)
  }

  const handleCloseWizard = () => {
    setIsWizardOpen(false)
    setWizardType(null)
    setSelectedClusterForWizard(null)
  }

  const handleWizardComplete = (
    clusterKey: string,
    wizardType: 'target-infra' | 'migration-infra' | 'migration-scripts'
  ) => {
    // Close the wizard
    setIsWizardOpen(false)
    setWizardType(null)
    setSelectedClusterForWizard(null)

    // Open the relevant tab and expand the cluster
    setMigrationAssetTab(clusterKey, wizardType)
    setExpandedCluster(clusterKey)
  }

  const toggleCluster = (clusterKey: string) => {
    setExpandedCluster(expandedCluster === clusterKey ? null : clusterKey)
  }

  // Get stored terraform files from Zustand
  const migrationAssets = useAppStore((state) => state.migrationAssets)

  const getTerraformFiles = (
    clusterKey: string,
    wizardType: 'target-infra' | 'migration-infra' | 'migration-scripts'
  ) => {
    return migrationAssets[clusterKey]?.[wizardType] || null
  }

  const handleCopyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
  }

  const createZipBlob = async (files: Record<string, string | undefined>): Promise<Blob> => {
    // Use JSZip if available, otherwise create a simple archive structure
    try {
      const { default: JSZip } = await import('jszip')
      const zip = new JSZip()

      for (const [key, content] of Object.entries(files)) {
        if (content) {
          const fileName = key.replace('_', '.')
          zip.file(fileName, content)
        }
      }

      return await zip.generateAsync({ type: 'blob' })
    } catch {
      // Fallback: create individual files
      throw new Error('Failed to create zip file')
    }
  }

  const handleSaveLocally = async (
    files: Record<string, string | undefined>,
    clusterName: string,
    wizardType: string
  ) => {
    try {
      // Check if File System API is supported
      if (!('showDirectoryPicker' in window)) {
        alert(
          'Your browser does not support saving files to specific locations. Please use "Download ZIP" instead.'
        )
        return
      }

      // Filter files with content
      const fileEntries = Object.entries(files).filter(([, content]) => content)

      if (fileEntries.length === 0) {
        alert('No files to download')
        return
      }

      // Create zip blob
      const blob = await createZipBlob(files)
      const zipFileName = `${clusterName}-${wizardType}.zip`

      // Use File System API to save to user-selected directory
      const directoryHandle = await (window as any).showDirectoryPicker({
        mode: 'readwrite',
        startIn: 'downloads',
      })

      // Save the zip file to the selected directory
      const fileHandle = await directoryHandle.getFileHandle(zipFileName, { create: true })
      const writable = await fileHandle.createWritable()
      await writable.write(blob)
      await writable.close()

      alert(`Successfully saved ${zipFileName} to your selected directory!`)
    } catch (error: any) {
      // User canceled the picker or other error
      if (error.name === 'AbortError' || error.message === 'The user aborted a request.') {
        // User canceled directory selection
      } else if (
        error.message?.includes('system files') ||
        error.code === 'InvalidModificationError'
      ) {
        // Error saving to selected directory - error handling done in catch block
        alert(
          'Cannot save to this directory. Please select a different folder (e.g., Desktop, Documents, or a subfolder).'
        )
      } else {
        // Failed to save files - error handling done in catch block
        alert('Failed to save files. Please try again or use "Download ZIP" instead.')
      }
    }
  }

  const handleDownloadZip = async (
    files: Record<string, string | undefined>,
    clusterName: string,
    wizardType: string
  ) => {
    try {
      // Filter files with content
      const fileEntries = Object.entries(files).filter(([, content]) => content)

      if (fileEntries.length === 0) {
        alert('No files to download')
        return
      }

      // Dynamically import JSZip only when needed
      const { default: JSZip } = await import('jszip')

      // Create zip file
      const zip = new JSZip()

      // Add files to zip
      for (const [key, content] of fileEntries) {
        if (content) {
          const fileName = key.replace('_', '.')
          zip.file(fileName, content)
        }
      }

      // Generate zip blob
      const blob = await zip.generateAsync({ type: 'blob' })
      const zipFileName = `${clusterName}-${wizardType}.zip`

      // Download directly to browser's download folder
      const url = window.URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = zipFileName
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      window.URL.revokeObjectURL(url)
    } catch {
      // Failed to create zip file - error handling done in catch block
      alert('Failed to create zip file. Please try again.')
    }
  }

  const renderTerraformTabs = (
    clusterKey: string,
    wizardType: 'target-infra' | 'migration-infra' | 'migration-scripts',
    clusterName: string
  ) => {
    const files = getTerraformFiles(clusterKey, wizardType)
    if (!files) {
      return <p className="text-gray-600 dark:text-gray-400">No terraform files generated yet.</p>
    }

    // Get all file entries dynamically
    const fileEntries = Object.entries(files).filter(([, content]) => content)

    if (fileEntries.length === 0) {
      return <p className="text-gray-600 dark:text-gray-400">No terraform files available.</p>
    }

    const fileTabsKey = `${clusterKey}-${wizardType}`
    const activeFileTab = activeFileTabs[fileTabsKey] || fileEntries[0][0]

    // Get the content of the currently active file
    const activeContent = fileEntries.find(([key]) => key === activeFileTab)?.[1] || ''

    return (
      <div className="space-y-4">
        {/* File Tabs Navigation */}
        <div className="border-b border-gray-200 dark:border-border">
          <div className="flex items-center justify-between">
            <nav className="-mb-px flex space-x-2 overflow-x-auto px-4 flex-1">
              {fileEntries.map(([key]) => (
                <button
                  key={key}
                  onClick={() => setActiveFileTabs((prev) => ({ ...prev, [fileTabsKey]: key }))}
                  className={`py-3 px-4 border-b-2 font-medium text-sm transition-colors whitespace-nowrap ${
                    activeFileTab === key
                      ? 'border-green-500 text-green-600 dark:text-green-400'
                      : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-gray-300 dark:hover:border-[#4A4956]'
                  }`}
                >
                  {key.replace('_', '.')}
                </button>
              ))}
            </nav>
            <div className="flex items-center gap-1.5 px-2 shrink-0">
              <Button
                size="sm"
                variant="outline"
                onClick={() => handleCopyToClipboard(activeContent)}
                className="text-xs px-2 py-1"
              >
                📋 Copy
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => handleDownloadZip(files, clusterName, wizardType)}
                className="text-xs px-2 py-1 bg-blue-600 hover:bg-blue-700 text-white border-blue-600"
              >
                💾 Download ZIP
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => handleSaveLocally(files, clusterName, wizardType)}
                className="text-xs px-2 py-1 bg-green-600 hover:bg-green-700 text-white border-green-600"
              >
                📁 Save Locally
              </Button>
            </div>
          </div>
        </div>

        {/* File Content */}
        <div className="mt-4">
          {fileEntries.map(([key, content]) => {
            if (activeFileTab === key && content) {
              return (
                <TerraformCodeViewer
                  key={key}
                  code={content}
                  language="terraform"
                />
              )
            }
            return null
          })}
        </div>
      </div>
    )
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Migration Assets</h1>
        <p className="text-gray-600 dark:text-gray-400 mt-2">
          Manage and track your migration assets and resources for all clusters.
        </p>
      </div>

      {allClusters.length > 0 ? (
        <div className="space-y-4">
          {allClusters.map(({ cluster, regionName }) => {
            const clusterKey = `${regionName}-${cluster.name}`

            const isExpanded = expandedCluster === clusterKey

            return (
              <div
                key={clusterKey}
                className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border overflow-hidden"
              >
                {/* Cluster Header Row - Clickable */}
                <div
                  className="px-6 py-4 border-b border-gray-200 dark:border-border cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700 transition-colors"
                  onClick={() => toggleCluster(clusterKey)}
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center space-x-6">
                      <div className="flex items-center space-x-3">
                        <span className="text-gray-400 dark:text-gray-500">
                          {isExpanded ? '▼' : '▶'}
                        </span>
                        <h3 className="text-lg font-medium text-gray-900 dark:text-gray-100">
                          {cluster.name}
                        </h3>
                      </div>
                    </div>
                    <div
                      className="flex items-center space-x-2"
                      onClick={(e) => e.stopPropagation()}
                    >
                      <Button
                        variant="outline"
                        size="sm"
                        className="bg-green-600 hover:bg-green-700 text-white border-green-600"
                        onClick={() => handleCreateMigrationInfrastructure(cluster, regionName)}
                      >
                        Create Migration Infrastructure
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        className="bg-blue-600 hover:bg-blue-700 text-white"
                        onClick={() => handleCreateTargetInfrastructure(cluster, regionName)}
                      >
                        Create Target Infrastructure
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        className="bg-purple-600 hover:bg-purple-700 text-white border-purple-600"
                        onClick={() => handleCreateMigrationScripts(cluster, regionName)}
                      >
                        Create Migration Scripts
                      </Button>
                    </div>
                  </div>
                </div>

                {/* Tabbed Content - Collapsible */}
                {isExpanded && (
                  <div className="border-t border-gray-200 dark:border-border bg-gray-50 dark:bg-card">
                    <div className="border-b border-gray-200 dark:border-border">
                      <nav className="-mb-px flex space-x-8 px-6 overflow-x-auto bg-white dark:bg-card">
                        {[
                          { id: 'migration-infra', label: 'Migration Infrastructure' },
                          { id: 'target-infra', label: 'Target Infrastructure' },
                          { id: 'migration-scripts', label: 'Migration Scripts' },
                        ].map((tab) => (
                          <button
                            key={tab.id}
                            onClick={() => setMigrationAssetTab(clusterKey, tab.id)}
                            className={`py-4 px-1 border-b-2 font-medium text-sm transition-colors whitespace-nowrap ${
                              migrationAssetTabs[clusterKey] === tab.id
                                ? 'border-blue-500 dark:border-accent text-blue-600 dark:text-accent'
                                : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-gray-300 dark:hover:border-border'
                            }`}
                          >
                            {tab.label}
                          </button>
                        ))}
                      </nav>
                    </div>
                    <div className="p-6">
                      {/* Migration Infrastructure Tab */}
                      {migrationAssetTabs[clusterKey] === 'migration-infra' && (
                        <div>
                          {renderTerraformTabs(clusterKey, 'migration-infra', cluster.name)}
                        </div>
                      )}
                      {/* Target Infrastructure Tab */}
                      {migrationAssetTabs[clusterKey] === 'target-infra' && (
                        <div>{renderTerraformTabs(clusterKey, 'target-infra', cluster.name)}</div>
                      )}
                      {/* Migration Scripts Tab */}
                      {migrationAssetTabs[clusterKey] === 'migration-scripts' && (
                        <div>
                          {renderTerraformTabs(clusterKey, 'migration-scripts', cluster.name)}
                        </div>
                      )}
                      {/* Default view when no tab is active */}
                      {!migrationAssetTabs[clusterKey] && (
                        <div>
                          {renderTerraformTabs(clusterKey, 'migration-infra', cluster.name)}
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      ) : (
        <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-8">
          <div className="text-center">
            <div className="mx-auto w-16 h-16 bg-gray-100 dark:bg-card rounded-full flex items-center justify-center mb-4">
              <span className="text-2xl">📦</span>
            </div>
            <h2 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-2">
              No Clusters Available
            </h2>
            <p className="text-gray-600 dark:text-gray-400">
              Upload a KCP state file to see your clusters and manage migration assets.
            </p>
          </div>
        </div>
      )}

      {/* Migration Wizard Modal */}
      {selectedClusterForWizard && wizardType && (
        <Modal
          isOpen={isWizardOpen}
          onClose={handleCloseWizard}
          title={`${
            wizardType === 'target-infra'
              ? 'Create Target Infrastructure'
              : wizardType === 'migration-infra'
              ? 'Create Migration Infrastructure'
              : 'Create Migration Scripts'
          } - ${selectedClusterForWizard.cluster.name}`}
        >
          {wizardType === 'target-infra' && selectedClusterForWizard && (
            <Wizard
              config={targetInfraWizardConfig}
              clusterKey={`${selectedClusterForWizard.regionName}-${selectedClusterForWizard.cluster.name}`}
              wizardType={wizardType}
              onComplete={() =>
                handleWizardComplete(
                  `${selectedClusterForWizard.regionName}-${selectedClusterForWizard.cluster.name}`,
                  wizardType
                )
              }
            />
          )}
          {wizardType === 'migration-infra' && selectedClusterForWizard && (
            <Wizard
              config={migrationInfraWizardConfig}
              clusterKey={`${selectedClusterForWizard.regionName}-${selectedClusterForWizard.cluster.name}`}
              wizardType={wizardType}
              onComplete={() =>
                handleWizardComplete(
                  `${selectedClusterForWizard.regionName}-${selectedClusterForWizard.cluster.name}`,
                  wizardType
                )
              }
            />
          )}
          {wizardType === 'migration-scripts' && selectedClusterForWizard && (
            <Wizard
              config={migrationScriptsWizardConfig}
              clusterKey={`${selectedClusterForWizard.regionName}-${selectedClusterForWizard.cluster.name}`}
              wizardType={wizardType}
              onComplete={() =>
                handleWizardComplete(
                  `${selectedClusterForWizard.regionName}-${selectedClusterForWizard.cluster.name}`,
                  wizardType
                )
              }
            />
          )}
        </Modal>
      )}
    </div>
  )
}
