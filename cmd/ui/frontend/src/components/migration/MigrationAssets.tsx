import { useState, useEffect } from 'react'
import { useAppStore, useRegions } from '@/stores/store'
import { Modal } from '@/components/common/ui/modal'
import { Package } from 'lucide-react'
import {
  Wizard,
  createTargetInfraWizardConfig,
  createMigrationInfraWizardConfig,
} from '@/components/migration/wizards'
import type { Cluster, WizardType } from '@/types'
import { WIZARD_TYPES } from '@/constants'
import { getClusterArn } from '@/lib/clusterUtils'
import { getWizardTitle, getWizardFilesTitle } from '@/lib/wizardUtils'
import { MigrationFlow } from './MigrationFlow'
import { ClusterAccordion } from './ClusterAccordion'
import { TerraformFileViewer } from './TerraformFileViewer'
import { MigrationScriptsSelection } from './MigrationScriptsSelection'
import { MigrationScriptsFileViewer } from './MigrationScriptsFileViewer'

export const MigrationAssets = () => {
  const regions = useRegions()
  const [isWizardOpen, setIsWizardOpen] = useState(false)
  const [wizardType, setWizardType] = useState<WizardType | null>(null)
  const [selectedClusterForWizard, setSelectedClusterForWizard] = useState<{
    cluster: Cluster
    regionName: string
  } | null>(null)

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
      const firstClusterArn = getClusterArn(allClusters[0].cluster)
      if (firstClusterArn) {
        setExpandedCluster(firstClusterArn)
      }
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

  const handleMigrationScriptsComplete = (selectedWizardType: WizardType) => {
    if (selectedClusterForWizard) {
      const clusterArn = getClusterArn(selectedClusterForWizard.cluster)
      if (clusterArn) {
        handleWizardComplete(clusterArn, selectedWizardType, selectedClusterForWizard.cluster.name)
      }
    }
  }

  const handleWizardComplete = (clusterKey: string, wizardType: WizardType, clusterName: string) => {
    // Close the wizard
    setIsWizardOpen(false)
    setWizardType(null)
    setSelectedClusterForWizard(null)

    // Expand the cluster
    setExpandedCluster(clusterKey)

    // Open the file viewer modal to show the generated Terraform files
    handleViewTerraform(clusterKey, wizardType, clusterName)
  }

  const toggleCluster = (clusterKey: string) => {
    setExpandedCluster(expandedCluster === clusterKey ? null : clusterKey)
  }

  // Get stored terraform files from Zustand
  const migrationAssets = useAppStore((state) => state.migrationAssets)

  const getTerraformFiles = (clusterKey: string, wizardType: WizardType) => {
    return migrationAssets[clusterKey]?.[wizardType] || null
  }

  const getPhaseStatus = (clusterKey: string, wizardType: WizardType): 'completed' | 'pending' => {
    // For MIGRATION_SCRIPTS, check if any of the sub-types have files
    if (wizardType === WIZARD_TYPES.MIGRATION_SCRIPTS) {
      const hasSchemas = getTerraformFiles(clusterKey, WIZARD_TYPES.MIGRATE_SCHEMAS)
      const hasTopics = getTerraformFiles(clusterKey, WIZARD_TYPES.MIGRATE_TOPICS)
      const hasAcls = getTerraformFiles(clusterKey, WIZARD_TYPES.MIGRATE_ACLS)
      return (hasSchemas || hasTopics || hasAcls) ? 'completed' : 'pending'
    }
    
    const files = getTerraformFiles(clusterKey, wizardType)
    return files ? 'completed' : 'pending'
  }

  const handleViewTerraform = (clusterKey: string, wizardType: WizardType, clusterName: string) => {
    setFileViewerModal({
      isOpen: true,
      clusterKey,
      wizardType,
      clusterName,
    })
  }

  const shouldShowFileViewerModal =
    fileViewerModal.isOpen &&
    fileViewerModal.clusterKey &&
    fileViewerModal.wizardType &&
    fileViewerModal.clusterName

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
            const clusterArn = getClusterArn(cluster)
            if (!clusterArn) return null // Skip clusters without ARN

            const isExpanded = expandedCluster === clusterArn

            return (
              <ClusterAccordion
                key={clusterArn}
                cluster={cluster}
                isExpanded={isExpanded}
                onToggle={() => toggleCluster(clusterArn)}
              >
                <MigrationFlow
                  clusterKey={clusterArn}
                  cluster={cluster}
                  regionName={regionName}
                  getPhaseStatus={getPhaseStatus}
                  onCreateTargetInfrastructure={handleCreateTargetInfrastructure}
                  onCreateMigrationInfrastructure={handleCreateMigrationInfrastructure}
                  onCreateMigrationScripts={handleCreateMigrationScripts}
                  onViewTerraform={handleViewTerraform}
                />
              </ClusterAccordion>
            )
          })}
        </div>
      ) : (
        <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border p-8">
          <div className="text-center">
            <div className="mx-auto w-16 h-16 bg-gray-100 dark:bg-card rounded-full flex items-center justify-center mb-4">
              <Package className="h-8 w-8 text-gray-400 dark:text-gray-500" />
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
      {selectedClusterForWizard &&
        wizardType &&
        (() => {
          const clusterArn = getClusterArn(selectedClusterForWizard.cluster) || ''
          return (
            <Modal
              isOpen={isWizardOpen}
              onClose={handleCloseWizard}
              title={`${wizardType ? getWizardTitle(wizardType) : ''} - ${
                selectedClusterForWizard.cluster.name
              }`}
            >
              {wizardType === WIZARD_TYPES.TARGET_INFRA && (
                <Wizard
                  config={createTargetInfraWizardConfig(clusterArn)}
                  clusterKey={clusterArn}
                  wizardType={wizardType}
                  onComplete={() => {
                    if (clusterArn) {
                      handleWizardComplete(clusterArn, WIZARD_TYPES.TARGET_INFRA, selectedClusterForWizard.cluster.name)
                    }
                  }}
                  onClose={handleCloseWizard}
                />
              )}
              {wizardType === WIZARD_TYPES.MIGRATION_INFRA && (
                <Wizard
                  config={createMigrationInfraWizardConfig(clusterArn)}
                  clusterKey={clusterArn}
                  wizardType={wizardType}
                  onComplete={() => {
                    if (clusterArn) {
                      handleWizardComplete(clusterArn, WIZARD_TYPES.MIGRATION_INFRA, selectedClusterForWizard.cluster.name)
                    }
                  }}
                  onClose={handleCloseWizard}
                />
              )}
              {wizardType === WIZARD_TYPES.MIGRATION_SCRIPTS && (
                <MigrationScriptsSelection
                  clusterArn={clusterArn}
                  onComplete={handleMigrationScriptsComplete}
                  onClose={handleCloseWizard}
                  hasGeneratedFiles={(wizardType) => !!getTerraformFiles(clusterArn, wizardType)}
                  onViewTerraform={(wizardType) => {
                    // Close the wizard modal and open the file viewer modal
                    handleCloseWizard()
                    handleViewTerraform(clusterArn, wizardType, selectedClusterForWizard.cluster.name)
                  }}
                />
              )}
            </Modal>
          )
        })()}

      {/* File Viewer Modal */}
      {shouldShowFileViewerModal && (
        <Modal
          isOpen={true}
          onClose={() =>
            setFileViewerModal({
              isOpen: false,
              clusterKey: null,
              wizardType: null,
              clusterName: null,
            })
          }
          title={`${
            fileViewerModal.wizardType ? getWizardFilesTitle(fileViewerModal.wizardType) : ''
          } - ${fileViewerModal.clusterName}`}
          className="[&>div>div:last-child]:overflow-hidden [&>div>div:last-child>div]:overflow-hidden [&>div>div:last-child>div]:p-0"
        >
          <div className="w-full h-full">
            {fileViewerModal.clusterKey &&
              fileViewerModal.wizardType &&
              fileViewerModal.clusterName && (
                <>
                  {fileViewerModal.wizardType === WIZARD_TYPES.MIGRATION_SCRIPTS ? (
                    <MigrationScriptsFileViewer
                      clusterKey={fileViewerModal.clusterKey}
                      clusterName={fileViewerModal.clusterName}
                      getTerraformFiles={getTerraformFiles}
                    />
                  ) : (
                    <TerraformFileViewer
                      files={getTerraformFiles(fileViewerModal.clusterKey, fileViewerModal.wizardType)}
                      clusterName={fileViewerModal.clusterName}
                      wizardType={fileViewerModal.wizardType}
                    />
                  )}
                </>
              )}
          </div>
        </Modal>
      )}
    </div>
  )
}
