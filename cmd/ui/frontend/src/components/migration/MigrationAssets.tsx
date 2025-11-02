import React, { useState, useEffect } from 'react'
import { useAppStore } from '@/stores/store'
import { Modal } from '@/components/common/ui/modal'
import { Button } from '@/components/common/ui/button'
import { TerraformCodeViewer } from './TerraformCodeViewer'
import {
  Wizard,
  targetInfraWizardConfig,
  migrationInfraWizardConfig,
  migrationScriptsWizardConfig,
} from '@/components/migration/wizards'
import type { Cluster, WizardType } from '@/types'
import { WIZARD_TYPES } from '@/constants'
import { Server, Network, Code, CheckCircle2, ArrowRight } from 'lucide-react'

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
  const [wizardType, setWizardType] = useState<WizardType | null>(null)
  const [selectedClusterForWizard, setSelectedClusterForWizard] = useState<{
    cluster: Cluster
    regionName: string
  } | null>(null)

  // Track active tab for each cluster (persisted in store)
  const migrationAssetTabs = useAppStore((state) => state.migrationAssetTabs)
  const setMigrationAssetTab = useAppStore((state) => state.setMigrationAssetTab)
  const [activeFileTabs, setActiveFileTabs] = useState<Record<string, string>>({})
  // Track which cluster section is expanded (persisted in store)
  const expandedCluster = useAppStore((state) => state.expandedMigrationCluster)
  const setExpandedCluster = useAppStore((state) => state.setExpandedMigrationCluster)
  // Track file viewer modal state
  const [fileViewerModal, setFileViewerModal] = useState<{
    isOpen: boolean
    clusterKey: string | null
    wizardType: WizardType | null
    clusterName: string | null
  }>({
    isOpen: false,
    clusterKey: null,
    wizardType: null,
    clusterName: null,
  })

  // Flatten all clusters from all regions
  const allClusters = regions.flatMap((region) =>
    (region.clusters || [])
      .filter((cluster) => cluster.aws_client_information?.msk_cluster_config?.Provisioned)
      .map((cluster) => ({
        cluster,
        regionName: region.name,
      }))
  )

  // Expand first cluster by default when clusters are loaded
  useEffect(() => {
    if (allClusters.length > 0 && !expandedCluster) {
      const firstClusterKey = `${allClusters[0].regionName}-${allClusters[0].cluster.name}`
      setExpandedCluster(firstClusterKey)
    }
  }, [allClusters, expandedCluster, setExpandedCluster])

  const handleCreateTargetInfrastructure = (cluster: Cluster, regionName: string) => {
    setSelectedClusterForWizard({ cluster, regionName })
    setWizardType(WIZARD_TYPES.TARGET_INFRA)
    setIsWizardOpen(true)
  }

  const handleCreateMigrationInfrastructure = (cluster: Cluster, regionName: string) => {
    setSelectedClusterForWizard({ cluster, regionName })
    setWizardType(WIZARD_TYPES.MIGRATION_INFRA)
    setIsWizardOpen(true)
  }

  const handleCreateMigrationScripts = (cluster: Cluster, regionName: string) => {
    setSelectedClusterForWizard({ cluster, regionName })
    setWizardType(WIZARD_TYPES.MIGRATION_SCRIPTS)
    setIsWizardOpen(true)
  }

  const handleCloseWizard = () => {
    setIsWizardOpen(false)
    setWizardType(null)
    setSelectedClusterForWizard(null)
  }

  const handleWizardComplete = (clusterKey: string, wizardType: WizardType) => {
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

  const getTerraformFiles = (clusterKey: string, wizardType: WizardType) => {
    return migrationAssets[clusterKey]?.[wizardType] || null
  }

  const getPhaseStatus = (clusterKey: string, wizardType: WizardType) => {
    const files = getTerraformFiles(clusterKey, wizardType)
    return files ? 'completed' : 'pending'
  }

  const renderMigrationFlow = (clusterKey: string, cluster: Cluster, regionName: string) => {
    const phases = [
      {
        step: 1,
        id: WIZARD_TYPES.TARGET_INFRA,
        title: 'Target Infrastructure',
        description: 'Set up your target infrastructure',
        icon: Server,
        handler: () => handleCreateTargetInfrastructure(cluster, regionName),
      },
      {
        step: 2,
        id: WIZARD_TYPES.MIGRATION_INFRA,
        title: 'Migration Infrastructure',
        description: 'Configure migration infrastructure',
        icon: Network,
        handler: () => handleCreateMigrationInfrastructure(cluster, regionName),
      },
      {
        step: 3,
        id: WIZARD_TYPES.MIGRATION_SCRIPTS,
        title: 'Migration Scripts',
        description: 'Generate migration automation scripts',
        icon: Code,
        handler: () => handleCreateMigrationScripts(cluster, regionName),
      },
    ]

    return (
      <div className="py-6 px-6">
        <div className="mb-6">
          <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-1">
            Migration Flow
          </h3>
          <p className="text-xs text-gray-500 dark:text-gray-400">
            Complete each phase in order from left to right
          </p>
        </div>
        <div className="flex items-stretch justify-between gap-4">
          {phases.map((phase, index) => {
            const status = getPhaseStatus(clusterKey, phase.id)
            const isCompleted = status === 'completed'
            const Icon = phase.icon

            return (
              <React.Fragment key={phase.id}>
                <div className="flex items-stretch flex-1">
                  {/* Phase Card */}
                  <div
                    className={`flex-1 relative flex flex-col items-center p-6 rounded-lg border-2 transition-all bg-white dark:bg-card hover:border-gray-300 dark:hover:border-gray-600 h-full ${
                      isCompleted ? 'border-accent' : 'border-gray-200 dark:border-border'
                    }`}
                  >
                    {/* Step Number Badge */}
                    <div
                      className={`absolute -top-3 -left-3 w-8 h-8 rounded-full flex items-center justify-center font-bold text-sm border-2 ${
                        isCompleted
                          ? 'bg-white dark:bg-card text-gray-700 dark:text-gray-300 border-accent'
                          : 'bg-white dark:bg-card text-gray-700 dark:text-gray-300 border-accent'
                      }`}
                    >
                      {phase.step}
                    </div>

                    {/* Icon */}
                    <div className="mb-4 p-3 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400">
                      <Icon className="w-6 h-6" />
                    </div>

                    {/* Title */}
                    <h4
                      className={`text-sm font-semibold mb-1 text-center flex items-center gap-1.5 justify-center ${
                        isCompleted ? 'text-accent' : 'text-gray-900 dark:text-gray-100'
                      }`}
                    >
                      {phase.title}
                      {isCompleted && (
                        <CheckCircle2 className="w-4 h-4 text-green-500 dark:text-green-400 flex-shrink-0" />
                      )}
                    </h4>

                    {/* Description */}
                    <p className="text-xs text-gray-500 dark:text-gray-400 text-center mb-4">
                      {phase.description}
                    </p>

                    {/* Action Buttons */}
                    {isCompleted ? (
                      <div className="flex gap-2 w-full">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => phase.handler()}
                          className="flex-1"
                        >
                          Start Over
                        </Button>
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => {
                            setFileViewerModal({
                              isOpen: true,
                              clusterKey,
                              wizardType: phase.id,
                              clusterName: cluster.name,
                            })
                          }}
                          className="flex-1"
                        >
                          View Files
                        </Button>
                      </div>
                    ) : (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => phase.handler()}
                        className="w-auto"
                      >
                        {phase.id === WIZARD_TYPES.MIGRATION_SCRIPTS
                          ? 'Generate Scripts'
                          : 'Generate Terraform'}
                      </Button>
                    )}
                  </div>
                </div>

                {/* Connector Arrow */}
                {index < phases.length - 1 && (
                  <div className="px-2 flex-shrink-0 flex items-center">
                    <ArrowRight
                      className={`w-5 h-5 ${
                        isCompleted
                          ? 'text-green-500 dark:text-green-600'
                          : 'text-gray-300 dark:text-gray-600'
                      }`}
                    />
                  </div>
                )}
              </React.Fragment>
            )
          })}
        </div>
      </div>
    )
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
      const directoryHandle = await window.showDirectoryPicker({
        mode: 'readwrite',
        startIn: 'downloads',
      })

      // Save the zip file to the selected directory
      const fileHandle = await directoryHandle.getFileHandle(zipFileName, { create: true })
      const writable = await fileHandle.createWritable()
      await writable.write(blob)
      await writable.close()

      alert(`Successfully saved ${zipFileName} to your selected directory!`)
    } catch (error: unknown) {
      // User canceled the picker or other error
      const err = error as { name?: string; message?: string; code?: string }
      if (err.name === 'AbortError' || err.message === 'The user aborted a request.') {
        // User canceled directory selection
      } else if (err.message?.includes('system files') || err.code === 'InvalidModificationError') {
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

  const renderTerraformTabs = (clusterKey: string, wizardType: WizardType, clusterName: string) => {
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
                      ? 'border-accent text-accent'
                      : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-gray-300 dark:hover:border-border'
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
                className="text-xs px-2 py-1"
              >
                💾 Download ZIP
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => handleSaveLocally(files, clusterName, wizardType)}
                className="text-xs px-2 py-1"
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
                className={`bg-white dark:bg-card rounded-lg border overflow-hidden transition-all ${
                  isExpanded
                    ? 'border-accent shadow-md dark:border-accent'
                    : 'border-gray-200 dark:border-border'
                }`}
              >
                {/* Cluster Header Row - Clickable */}
                <div
                  className={`px-6 py-4 border-b border-gray-200 dark:border-border cursor-pointer transition-colors ${
                    isExpanded
                      ? 'bg-accent/5 dark:bg-accent/10'
                      : 'hover:bg-gray-50 dark:hover:bg-gray-700'
                  }`}
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
                  </div>
                </div>

                {/* Migration Flow - Only shown when expanded */}
                {isExpanded && (
                  <div className="border-t border-gray-200 dark:border-border bg-gray-50 dark:bg-card overflow-visible pt-4">
                    {renderMigrationFlow(clusterKey, cluster, regionName)}
                  </div>
                )}

                {/* Tabbed Content - Collapsible - Only shown when expanded */}
                {isExpanded && (
                  <div className="border-t border-gray-200 dark:border-border bg-gray-50 dark:bg-card">
                    <div className="border-b border-gray-200 dark:border-border">
                      <nav className="-mb-px flex space-x-8 px-6 overflow-x-auto bg-white dark:bg-card">
                        {[
                          { id: WIZARD_TYPES.TARGET_INFRA, label: 'Target Infrastructure' },
                          { id: WIZARD_TYPES.MIGRATION_INFRA, label: 'Migration Infrastructure' },
                          { id: WIZARD_TYPES.MIGRATION_SCRIPTS, label: 'Migration Scripts' },
                        ].map((tab) => (
                          <button
                            key={tab.id}
                            onClick={() => setMigrationAssetTab(clusterKey, tab.id)}
                            className={`py-4 px-1 border-b-2 font-medium text-sm transition-colors whitespace-nowrap ${
                              migrationAssetTabs[clusterKey] === tab.id
                                ? 'border-accent text-accent'
                                : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-gray-300 dark:hover:border-border'
                            }`}
                          >
                            {tab.label}
                          </button>
                        ))}
                      </nav>
                    </div>
                    <div className="p-6">
                      {/* Target Infrastructure Tab */}
                      {migrationAssetTabs[clusterKey] === WIZARD_TYPES.TARGET_INFRA && (
                        <div>
                          {renderTerraformTabs(clusterKey, WIZARD_TYPES.TARGET_INFRA, cluster.name)}
                        </div>
                      )}
                      {/* Migration Infrastructure Tab */}
                      {migrationAssetTabs[clusterKey] === WIZARD_TYPES.MIGRATION_INFRA && (
                        <div>
                          {renderTerraformTabs(
                            clusterKey,
                            WIZARD_TYPES.MIGRATION_INFRA,
                            cluster.name
                          )}
                        </div>
                      )}
                      {/* Migration Scripts Tab */}
                      {migrationAssetTabs[clusterKey] === WIZARD_TYPES.MIGRATION_SCRIPTS && (
                        <div>
                          {renderTerraformTabs(
                            clusterKey,
                            WIZARD_TYPES.MIGRATION_SCRIPTS,
                            cluster.name
                          )}
                        </div>
                      )}
                      {/* Default view when no tab is active */}
                      {!migrationAssetTabs[clusterKey] && (
                        <div>
                          {renderTerraformTabs(clusterKey, WIZARD_TYPES.TARGET_INFRA, cluster.name)}
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
            wizardType === WIZARD_TYPES.TARGET_INFRA
              ? 'Create Target Infrastructure'
              : wizardType === WIZARD_TYPES.MIGRATION_INFRA
              ? 'Create Migration Infrastructure'
              : 'Create Migration Scripts'
          } - ${selectedClusterForWizard.cluster.name}`}
        >
          {wizardType === WIZARD_TYPES.TARGET_INFRA && selectedClusterForWizard && (
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
              onClose={handleCloseWizard}
            />
          )}
          {wizardType === WIZARD_TYPES.MIGRATION_INFRA && selectedClusterForWizard && (
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
              onClose={handleCloseWizard}
            />
          )}
          {wizardType === WIZARD_TYPES.MIGRATION_SCRIPTS && selectedClusterForWizard && (
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
              onClose={handleCloseWizard}
            />
          )}
        </Modal>
      )}

      {/* File Viewer Modal */}
      {fileViewerModal.isOpen &&
        fileViewerModal.clusterKey &&
        fileViewerModal.wizardType &&
        fileViewerModal.clusterName && (
          <Modal
            isOpen={fileViewerModal.isOpen}
            onClose={() =>
              setFileViewerModal({
                isOpen: false,
                clusterKey: null,
                wizardType: null,
                clusterName: null,
              })
            }
            title={`${
              fileViewerModal.wizardType === WIZARD_TYPES.TARGET_INFRA
                ? 'Target Infrastructure Files'
                : fileViewerModal.wizardType === WIZARD_TYPES.MIGRATION_INFRA
                ? 'Migration Infrastructure Files'
                : 'Migration Scripts Files'
            } - ${fileViewerModal.clusterName}`}
          >
            <div className="max-w-4xl">
              {renderTerraformTabs(
                fileViewerModal.clusterKey,
                fileViewerModal.wizardType,
                fileViewerModal.clusterName
              )}
            </div>
          </Modal>
        )}
    </div>
  )
}
