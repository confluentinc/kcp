import { useState, useEffect } from 'react'
import { useAppStore, useRegions } from '@/stores/store'
import { Modal } from '@/components/common/ui/modal'
import { Package } from 'lucide-react'
import {
  Wizard,
  createTargetInfraWizardConfig,
  createMigrationInfraMskWizardConfig,
  createMigrationInfraApacheKafkaWizardConfig,
} from '@/components/migration/wizards'
import type { Cluster, WizardType, SourceType } from '@/types'
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
  const kcpState = useAppStore((state) => state.kcpState)
  const [isWizardOpen, setIsWizardOpen] = useState(false)
  const [wizardType, setWizardType] = useState<WizardType | null>(null)
  const [selectedClusterForWizard, setSelectedClusterForWizard] = useState<{
    cluster: Cluster | null
    clusterName: string
    regionName: string
    sourceType: SourceType
    clusterKey: string
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

  // Flatten all MSK clusters from all regions
  const mskClusters = regions.flatMap((region) =>
    (region.clusters || [])
      .filter((cluster) => cluster.aws_client_information?.msk_cluster_config?.Provisioned)
      .map((cluster) => ({
        cluster,
        regionName: region.name,
      }))
  )

  // Get Apache Kafka clusters from the state
  const apacheKafkaSource = kcpState?.sources.find(
    (s) => s.type === 'apache-kafka' && s.apache_kafka_data !== undefined
  )
  const apacheKafkaClusters = apacheKafkaSource?.apache_kafka_data?.clusters || []

  const hasMskClusters = mskClusters.length > 0
  const hasApacheKafkaClusters = apacheKafkaClusters.length > 0
  const hasAnyClusters = hasMskClusters || hasApacheKafkaClusters

  // Expand first cluster by default when clusters are loaded
  useEffect(() => {
    if (hasAnyClusters && !expandedCluster) {
      if (hasMskClusters) {
        const firstClusterArn = getClusterArn(mskClusters[0].cluster)
        if (firstClusterArn) {
          setExpandedCluster(firstClusterArn)
        }
      } else if (hasApacheKafkaClusters) {
        setExpandedCluster(apacheKafkaClusters[0].id)
      }
    }
  }, [hasAnyClusters, hasMskClusters, hasApacheKafkaClusters, expandedCluster, setExpandedCluster, mskClusters, apacheKafkaClusters])

  const openWizard = (
    wizardType: WizardType,
    clusterName: string,
    clusterKey: string,
    sourceType: SourceType,
    cluster: Cluster | null,
    regionName: string,
  ) => {
    setSelectedClusterForWizard({ cluster, clusterName, regionName, sourceType, clusterKey })
    setWizardType(wizardType)
    setIsWizardOpen(true)
  }

  // MSK handler factories
  const handleCreateTargetInfrastructureMsk = (cluster: Cluster, regionName: string) => {
    const arn = getClusterArn(cluster)
    if (arn) {
      openWizard(WIZARD_TYPES.TARGET_INFRA, cluster.name, arn, 'msk', cluster, regionName)
    }
  }

  const handleCreateMigrationInfrastructureMsk = (cluster: Cluster, regionName: string) => {
    const arn = getClusterArn(cluster)
    if (arn) {
      openWizard(WIZARD_TYPES.MIGRATION_INFRA, cluster.name, arn, 'msk', cluster, regionName)
    }
  }

  const handleCreateMigrationScriptsMsk = (cluster: Cluster, regionName: string) => {
    const arn = getClusterArn(cluster)
    if (arn) {
      openWizard(WIZARD_TYPES.MIGRATION_SCRIPTS, cluster.name, arn, 'msk', cluster, regionName)
    }
  }

  // Apache Kafka handler factories
  const handleCreateTargetInfrastructureApacheKafka = (clusterId: string, clusterName: string) => {
    openWizard(WIZARD_TYPES.TARGET_INFRA, clusterName, clusterId, 'apache-kafka', null, '')
  }

  const handleCreateMigrationInfrastructureApacheKafka = (clusterId: string, clusterName: string) => {
    openWizard(WIZARD_TYPES.MIGRATION_INFRA, clusterName, clusterId, 'apache-kafka', null, '')
  }

  const handleCreateMigrationScriptsApacheKafka = (clusterId: string, clusterName: string) => {
    openWizard(WIZARD_TYPES.MIGRATION_SCRIPTS, clusterName, clusterId, 'apache-kafka', null, '')
  }

  const handleCloseWizard = () => {
    setIsWizardOpen(false)
    setWizardType(null)
    setSelectedClusterForWizard(null)
  }

  const handleMigrationScriptsComplete = (selectedWizardType: WizardType) => {
    if (selectedClusterForWizard) {
      handleWizardComplete(
        selectedClusterForWizard.clusterKey,
        selectedWizardType,
        selectedClusterForWizard.clusterName
      )
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

  // Get the effective cluster key for the wizard modal
  const wizardClusterKey = selectedClusterForWizard?.clusterKey || ''

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-foreground">Migration Assets</h1>
        <p className="text-muted-foreground mt-2">
          Manage and track your migration assets and resources for all clusters.
        </p>
      </div>

      {hasAnyClusters ? (
        <div className="space-y-6">
          {/* MSK Clusters Section */}
          {hasMskClusters && (
            <div>
              <h2 className="text-lg font-semibold text-foreground mb-3">
                Managed Streaming for Kafka (MSK)
              </h2>
              <div className="space-y-4">
                {mskClusters.map(({ cluster, regionName }) => {
                  const clusterArn = getClusterArn(cluster)
                  if (!clusterArn) return null // Skip clusters without ARN

                  const isExpanded = expandedCluster === clusterArn

                  return (
                    <ClusterAccordion
                      key={clusterArn}
                      clusterName={cluster.name}
                      isExpanded={isExpanded}
                      onToggle={() => toggleCluster(clusterArn)}
                    >
                      <MigrationFlow
                        clusterKey={clusterArn}
                        clusterName={cluster.name}
                        getPhaseStatus={getPhaseStatus}
                        onCreateTargetInfrastructure={() => handleCreateTargetInfrastructureMsk(cluster, regionName)}
                        onCreateMigrationInfrastructure={() => handleCreateMigrationInfrastructureMsk(cluster, regionName)}
                        onCreateMigrationScripts={() => handleCreateMigrationScriptsMsk(cluster, regionName)}
                        onViewTerraform={handleViewTerraform}
                        migrationScriptsDescription="Generate Migration Assets to Move Data from MSK to Confluent Cloud"
                      />
                    </ClusterAccordion>
                  )
                })}
              </div>
            </div>
          )}

          {/* Apache Kafka Clusters Section */}
          {hasApacheKafkaClusters && (
            <div>
              <h2 className="text-lg font-semibold text-foreground mb-3">
                Apache Kafka
              </h2>
              <div className="space-y-4">
                {apacheKafkaClusters.map((apacheKafkaCluster) => {
                  const clusterKey = apacheKafkaCluster.id
                  const clusterName = apacheKafkaCluster.id
                  const isExpanded = expandedCluster === clusterKey

                  return (
                    <ClusterAccordion
                      key={clusterKey}
                      clusterName={clusterName}
                      isExpanded={isExpanded}
                      onToggle={() => toggleCluster(clusterKey)}
                    >
                      <MigrationFlow
                        clusterKey={clusterKey}
                        clusterName={clusterName}
                        getPhaseStatus={getPhaseStatus}
                        onCreateTargetInfrastructure={() => handleCreateTargetInfrastructureApacheKafka(apacheKafkaCluster.id, clusterName)}
                        onCreateMigrationInfrastructure={() => handleCreateMigrationInfrastructureApacheKafka(apacheKafkaCluster.id, clusterName)}
                        onCreateMigrationScripts={() => handleCreateMigrationScriptsApacheKafka(apacheKafkaCluster.id, clusterName)}
                        onViewTerraform={handleViewTerraform}
                        migrationScriptsDescription="Generate Migration Assets to Move Data from Kafka to Confluent Cloud"
                      />
                    </ClusterAccordion>
                  )
                })}
              </div>
            </div>
          )}
        </div>
      ) : (
        <div className="bg-card rounded-lg border border-border p-8">
          <div className="text-center">
            <div className="mx-auto w-16 h-16 bg-secondary rounded-full flex items-center justify-center mb-4">
              <Package className="h-8 w-8 text-muted-foreground" />
            </div>
            <h2 className="text-xl font-semibold text-foreground mb-2">
              No Clusters Available
            </h2>
            <p className="text-muted-foreground">
              Upload a KCP state file to see your clusters and manage migration assets.
            </p>
          </div>
        </div>
      )}

      {/* Migration Wizard Modal */}
      {selectedClusterForWizard &&
        wizardType &&
        (() => {
          return (
            <Modal
              isOpen={isWizardOpen}
              onClose={handleCloseWizard}
              title={`${wizardType ? getWizardTitle(wizardType) : ''} - ${
                selectedClusterForWizard.clusterName
              }`}
            >
              {wizardType === WIZARD_TYPES.TARGET_INFRA && (
                <Wizard
                  config={createTargetInfraWizardConfig(wizardClusterKey, selectedClusterForWizard.sourceType)}
                  clusterKey={wizardClusterKey}
                  wizardType={wizardType}
                  onComplete={() => {
                    if (wizardClusterKey) {
                      handleWizardComplete(wizardClusterKey, WIZARD_TYPES.TARGET_INFRA, selectedClusterForWizard.clusterName)
                    }
                  }}
                  onClose={handleCloseWizard}
                />
              )}
              {wizardType === WIZARD_TYPES.MIGRATION_INFRA && (
                <Wizard
                  config={
                    selectedClusterForWizard.sourceType === 'apache-kafka'
                      ? createMigrationInfraApacheKafkaWizardConfig(wizardClusterKey)
                      : createMigrationInfraMskWizardConfig(wizardClusterKey)
                  }
                  clusterKey={wizardClusterKey}
                  wizardType={wizardType}
                  onComplete={() => {
                    if (wizardClusterKey) {
                      handleWizardComplete(wizardClusterKey, WIZARD_TYPES.MIGRATION_INFRA, selectedClusterForWizard.clusterName)
                    }
                  }}
                  onClose={handleCloseWizard}
                />
              )}
              {wizardType === WIZARD_TYPES.MIGRATION_SCRIPTS && (
                <MigrationScriptsSelection
                  clusterArn={wizardClusterKey}
                  sourceType={selectedClusterForWizard.sourceType}
                  onComplete={handleMigrationScriptsComplete}
                  onClose={handleCloseWizard}
                  hasGeneratedFiles={(wizardType) => !!getTerraformFiles(wizardClusterKey, wizardType)}
                  onViewTerraform={(wizardType) => {
                    // Close the wizard modal and open the file viewer modal
                    handleCloseWizard()
                    handleViewTerraform(wizardClusterKey, wizardType, selectedClusterForWizard.clusterName)
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
