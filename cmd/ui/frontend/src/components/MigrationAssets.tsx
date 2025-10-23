import { useState } from 'react'
import { useAppStore } from '@/stores/appStore'
import { Modal } from './ui/modal'
import { Button } from './ui/button'
import {
  Wizard,
  targetInfraWizardConfig,
  migrationInfraWizardConfig,
  migrationScriptsWizardConfig,
} from './wizards'

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

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Migration Assets</h1>
        <p className="text-gray-600 dark:text-gray-400 mt-2">
          Manage and track your migration assets and resources for all clusters.
        </p>
      </div>

      {allClusters.length > 0 ? (
        <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700">
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead className="bg-gray-50 dark:bg-gray-700">
                <tr>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                    Cluster Name
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                    Region
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody className="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                {allClusters.map(({ cluster, regionName }) => (
                  <tr
                    key={`${regionName}-${cluster.name}`}
                    className="hover:bg-gray-50 dark:hover:bg-gray-700"
                  >
                    <td className="px-6 py-4 whitespace-nowrap">
                      <div className="text-sm font-medium text-gray-900 dark:text-gray-100">
                        {cluster.name}
                      </div>
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap">
                      <div className="text-sm text-gray-500 dark:text-gray-400">{regionName}</div>
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap">
                      <div className="flex space-x-2">
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
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-8">
          <div className="text-center">
            <div className="mx-auto w-16 h-16 bg-gray-100 dark:bg-gray-700 rounded-full flex items-center justify-center mb-4">
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
          {wizardType === 'target-infra' && <Wizard config={targetInfraWizardConfig} />}
          {wizardType === 'migration-infra' && <Wizard config={migrationInfraWizardConfig} />}
          {wizardType === 'migration-scripts' && <Wizard config={migrationScriptsWizardConfig} />}
        </Modal>
      )}
    </div>
  )
}
