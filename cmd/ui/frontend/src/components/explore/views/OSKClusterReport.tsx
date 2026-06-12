import { useState } from 'react'
import { useAppStore } from '@/stores/store'
import { isOSKSource } from '@/lib/sourceUtils'
import { OSKClusterHeader } from './OSKClusterHeader'
import { OSKClusterOverview } from './OSKClusterOverview'
import { ClusterTopics } from '../clusters/ClusterTopics'
import { ClusterACLs } from '../clusters/ClusterACLs'
import { ClusterConnectors } from '../clusters/ClusterConnectors'
import { ClusterClients } from '../clusters/ClusterClients'
import { ClusterMetrics } from '../clusters/ClusterMetrics'
import { Tabs } from '@/components/common/Tabs'

export const OSKClusterReport = () => {
  const kcpState = useAppStore((state) => state.kcpState)
  const selectedOSKClusterId = useAppStore((state) => state.selectedOSKClusterId)
  const [activeTab, setActiveTab] = useState('cluster')

  // Find the OSK source
  const oskSource = kcpState?.sources.find(isOSKSource)
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

  // Build tabs array
  const tabs = [
    { id: 'cluster', label: 'Cluster' },
    { id: 'metrics', label: 'Metrics' },
    { id: 'topics', label: 'Topics' },
    { id: 'acls', label: 'ACLs' },
    { id: 'connectors', label: 'Connectors' },
  ]

  if (cluster.discovered_clients && cluster.discovered_clients.length > 0) {
    tabs.push({ id: 'clients', label: 'Clients' })
  }

  return (
    <div className="w-full h-full flex flex-col min-h-0">
      {/* Fixed Header: Title, Stat Cards, Tabs */}
      <div className="bg-card border border-border border-b-0 rounded-t-lg shadow-sm flex-shrink-0 m-4 mb-0 transition-colors">
        <div className="p-6 border-b border-border">
          <OSKClusterHeader cluster={cluster} />
        </div>
        <Tabs tabs={tabs} activeId={activeTab} onChange={setActiveTab} className="border-b border-border" />
      </div>

      {/* Scrollable Tab Content */}
      <div className="flex-1 min-h-0 overflow-y-auto mx-4 mb-4 bg-card border border-border border-t-0 rounded-b-lg">
        <div className="p-6">
          {activeTab === 'cluster' && <OSKClusterOverview cluster={cluster} />}

          {activeTab === 'metrics' && selectedOSKClusterId && (
            <ClusterMetrics
              cluster={{ name: cluster.id, metrics: cluster.metrics }}
              sourceType="osk"
              clusterId={selectedOSKClusterId}
              isActive={activeTab === 'metrics'}
            />
          )}

          {activeTab === 'topics' && (
            <ClusterTopics kafkaAdminInfo={cluster.kafka_admin_client_information} />
          )}

          {activeTab === 'acls' && (
            <ClusterACLs acls={cluster.kafka_admin_client_information?.acls || []} />
          )}

          {activeTab === 'connectors' && (
            <ClusterConnectors
              connectors={[]}
              selfManagedConnectors={
                cluster.kafka_admin_client_information?.self_managed_connectors?.connectors
              }
            />
          )}

          {activeTab === 'clients' &&
            cluster.discovered_clients &&
            cluster.discovered_clients.length > 0 && (
              <ClusterClients clients={cluster.discovered_clients} />
            )}
        </div>
      </div>
    </div>
  )
}
