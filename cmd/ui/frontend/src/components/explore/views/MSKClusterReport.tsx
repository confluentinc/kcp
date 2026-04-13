import { useState } from 'react'
import { ClusterMetrics } from '../clusters/ClusterMetrics'
import { ClusterTopics } from '../clusters/ClusterTopics'
import { ClusterConnectors } from '../clusters/ClusterConnectors'
import { ClusterACLs } from '../clusters/ClusterACLs'
import { ClusterClients } from '../clusters/ClusterClients'
import { formatDate } from '@/lib/formatters'
import { Tabs } from '@/components/common/Tabs'
import { ClusterConfigurationSection } from '../clusters/ClusterConfigurationSection'
import { CLUSTER_REPORT_TABS } from '@/constants'
import type { ClusterReportTab } from '@/types'
import { getClusterArn } from '@/lib/clusterUtils'
import { useSelectedCluster, useRegions } from '@/stores/store'

export const MSKClusterReport = () => {
  const selectedClusterData = useSelectedCluster()
  const regions = useRegions()

  if (!selectedClusterData) {
    return (
      <div className="p-6">
        <div className="bg-destructive/10 border border-destructive/20 rounded-lg p-4">
          <p className="text-destructive">
            Cluster not found. Please select a cluster from the sidebar.
          </p>
        </div>
      </div>
    )
  }

  const { cluster, regionName } = selectedClusterData
  const regionData = regions.find((r) => r.name === regionName)
  const [activeTab, setActiveTab] = useState<ClusterReportTab>(CLUSTER_REPORT_TABS.CLUSTER)

  const mskConfig = cluster.aws_client_information?.msk_cluster_config
  const provisioned = mskConfig?.Provisioned
  const brokerInfo = provisioned?.BrokerNodeGroupInfo

  // Safety checks for required data
  if (!mskConfig || !provisioned || !brokerInfo) {
    return (
      <div className="w-full">
        <div className="bg-card rounded-lg shadow-sm border border-border p-6">
          <h1 className="text-2xl font-bold text-foreground">{cluster.name}</h1>
          <p className="text-muted-foreground mt-2">Cluster data is incomplete or unavailable.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="w-full h-full flex flex-col min-h-0">
      {/* Fixed Header: Title, Stat Cards, Tabs */}
      <div className="bg-card border border-border border-b-0 rounded-t-lg shadow-sm flex-shrink-0 m-4 mb-0 transition-colors">
        {/* Cluster Title */}
        <div className="p-6 border-b border-border">
          <div>
            <h1 className="text-2xl font-bold text-foreground">
              Cluster:&nbsp;{cluster.name}
            </h1>
            <div className="mt-2 space-y-1">
              {mskConfig.ClusterArn && (
                <p className="text-sm text-muted-foreground font-mono">
                  ARN: {mskConfig.ClusterArn}
                </p>
              )}
              <p className="text-sm text-muted-foreground">
                Created: {mskConfig.CreationTime ? formatDate(mskConfig.CreationTime) : 'Unknown'}
              </p>
            </div>
          </div>
        </div>

        {/* Tabs */}
        <Tabs
          tabs={[
            { id: CLUSTER_REPORT_TABS.CLUSTER, label: 'Cluster' },
            { id: CLUSTER_REPORT_TABS.METRICS, label: 'Metrics' },
            { id: CLUSTER_REPORT_TABS.TOPICS, label: 'Topics' },
            { id: CLUSTER_REPORT_TABS.CONNECTORS, label: 'Connectors' },
            { id: CLUSTER_REPORT_TABS.ACLS, label: 'ACLs' },
            { id: CLUSTER_REPORT_TABS.CLIENTS, label: 'Clients' },
          ]}
          activeId={activeTab}
          onChange={(id) => {
            setActiveTab(id as ClusterReportTab)
          }}
          className="border-b border-border"
        />
      </div>

      {/* Scrollable Tab Content */}
      <div className="flex-1 min-h-0 overflow-y-auto mx-4 mb-4 bg-card border border-border border-t-0 rounded-b-lg">
        <div className="p-6">
          {/* Metrics Tab */}
          {activeTab === CLUSTER_REPORT_TABS.METRICS && (
            <div className="min-w-0 max-w-full">
              {(() => {
                const clusterArn = getClusterArn(cluster) || cluster.arn
                if (!clusterArn) return null // Skip if no ARN
                return (
                  <ClusterMetrics
                    cluster={{
                      name: cluster.name,
                      region: regionName,
                      arn: clusterArn,
                    }}
                    isActive={activeTab === CLUSTER_REPORT_TABS.METRICS}
                  />
                )
              })()}
            </div>
          )}

          {/* Topics Tab */}
          {activeTab === CLUSTER_REPORT_TABS.TOPICS && (
            <div className="min-w-0 max-w-full">
              <ClusterTopics kafkaAdminInfo={cluster.kafka_admin_client_information} />
            </div>
          )}

          {/* Connectors Tab */}
          {activeTab === CLUSTER_REPORT_TABS.CONNECTORS && (
            <div className="min-w-0 max-w-full">
              <ClusterConnectors
                connectors={cluster.aws_client_information?.connectors || []}
                selfManagedConnectors={
                  cluster.kafka_admin_client_information?.self_managed_connectors?.connectors || []
                }
              />
            </div>
          )}

          {/* ACLs Tab */}
          {activeTab === CLUSTER_REPORT_TABS.ACLS && (
            <div className="min-w-0 max-w-full">
              <ClusterACLs acls={cluster.kafka_admin_client_information?.acls || []} />
            </div>
          )}

          {/* Clients Tab */}
          {activeTab === CLUSTER_REPORT_TABS.CLIENTS && (
            <div className="min-w-0 max-w-full">
              <ClusterClients clients={cluster.discovered_clients || []} />
            </div>
          )}

          {/* Cluster Configuration Tab */}
          {activeTab === CLUSTER_REPORT_TABS.CLUSTER && (
            <ClusterConfigurationSection
              cluster={cluster}
              provisioned={provisioned}
              brokerInfo={brokerInfo}
              regionData={regionData}
            />
          )}
        </div>
      </div>
    </div>
  )
}
