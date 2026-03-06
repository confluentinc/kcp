import { useAppStore } from '@/stores/store'
import { isOSKSource } from '@/lib/sourceUtils'
import { OSKClusterHeader } from './OSKClusterHeader'
import { OSKClusterOverview } from './OSKClusterOverview'
import { ClusterTopics } from '../clusters/ClusterTopics'
import { ClusterACLs } from '../clusters/ClusterACLs'
import { ClusterConnectors } from '../clusters/ClusterConnectors'
import { ClusterClients } from '../clusters/ClusterClients'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/common/Tabs'

export const OSKClusterReport = () => {
  const processedState = useAppStore((state) => state.processedState)
  const selectedOSKClusterId = useAppStore((state) => state.selectedOSKClusterId)

  // Find the OSK source
  const oskSource = processedState?.sources.find(isOSKSource)
  const cluster = oskSource?.osk_data?.clusters.find((c) => c.id === selectedOSKClusterId)

  if (!cluster) {
    return (
      <div className="p-6">
        <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-border rounded-lg p-4">
          <p className="text-red-800 dark:text-red-200">
            Cluster not found. Please select a cluster from the sidebar.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-6">
      <OSKClusterHeader cluster={cluster} />

      <Tabs defaultValue="cluster">
        <TabsList>
          <TabsTrigger value="cluster">Cluster</TabsTrigger>
          <TabsTrigger value="topics">Topics</TabsTrigger>
          <TabsTrigger value="acls">ACLs</TabsTrigger>
          <TabsTrigger value="connectors">Connectors</TabsTrigger>
          {cluster.discovered_clients && cluster.discovered_clients.length > 0 && (
            <TabsTrigger value="clients">Clients</TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="cluster">
          <OSKClusterOverview cluster={cluster} />
        </TabsContent>

        <TabsContent value="topics">
          <ClusterTopics topics={cluster.kafka_admin_client_information?.topics} />
        </TabsContent>

        <TabsContent value="acls">
          <ClusterACLs acls={cluster.kafka_admin_client_information?.acls} />
        </TabsContent>

        <TabsContent value="connectors">
          <ClusterConnectors
            connectors={cluster.kafka_admin_client_information?.self_managed_connectors}
          />
        </TabsContent>

        {cluster.discovered_clients && cluster.discovered_clients.length > 0 && (
          <TabsContent value="clients">
            <ClusterClients clients={cluster.discovered_clients} />
          </TabsContent>
        )}
      </Tabs>
    </div>
  )
}
