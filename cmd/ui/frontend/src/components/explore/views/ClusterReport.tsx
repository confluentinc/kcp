import { useState } from 'react'
import ClusterMetrics from '../clusters/ClusterMetrics'
import ClusterTopics from '../clusters/ClusterTopics'
import ClusterConnectors from '../clusters/ClusterConnectors'
import ClusterACLs from '../clusters/ClusterACLs'
import { formatDate } from '@/lib/formatters'
import Tabs from '@/components/common/Tabs'
import ClusterConfigurationSection from '../clusters/ClusterConfigurationSection'

interface ClusterReportProps {
  cluster: {
    name: string
    metrics?: {
      metadata: {
        cluster_type: string
        follower_fetching: boolean
        tiered_storage: boolean
        instance_type: string
        broker_az_distribution: string
        kafka_version: string
        enhanced_monitoring: string
        start_date: string
        end_date: string
        period: number // Period in seconds
      }
      results: Array<{
        start: string
        end: string
        label: string
        value: number | null
      }>
    }
    aws_client_information: {
      msk_cluster_config?: any
      connectors?: any[]
    }
    kafka_admin_client_information?: any
  }
  regionName: string
  regionData?: any
}

export default function ClusterReport({ cluster, regionName, regionData }: ClusterReportProps) {
  const [activeTab, setActiveTab] = useState<
    'metrics' | 'topics' | 'connectors' | 'cluster' | 'acls'
  >('cluster')

  const mskConfig = cluster.aws_client_information?.msk_cluster_config
  const provisioned = mskConfig?.Provisioned
  const brokerInfo = provisioned?.BrokerNodeGroupInfo

  // Safety checks for required data
  if (!mskConfig || !provisioned || !brokerInfo) {
    return (
      <div className="max-w-7xl mx-auto">
        <div className="bg-white rounded-lg shadow-sm border p-6">
          <h1 className="text-2xl font-bold text-gray-900">{cluster.name}</h1>
          <p className="text-gray-600 mt-2">Cluster data is incomplete or unavailable.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="max-w-7xl mx-auto space-y-6 min-w-0 w-full">
      {/* Header */}
      <div className="bg-white dark:bg-card rounded-lg shadow-sm border border-gray-200 dark:border-border transition-colors">
        {/* Cluster Title and Key Metrics */}
        <div className="p-6 border-b border-gray-200 dark:border-border">
          <div className="flex items-center justify-between mb-6">
            <div>
              <h1 className="text-3xl font-bold text-gray-900 dark:text-gray-100">
                Cluster:&nbsp;{cluster.name}
              </h1>
              <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
                Created: {mskConfig.CreationTime ? formatDate(mskConfig.CreationTime) : 'Unknown'} •
                Version: {mskConfig.CurrentVersion || 'Unknown'}
              </p>
            </div>
          </div>

          {/* Key Metrics */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <div className="bg-gray-50 dark:bg-card rounded-lg p-4 transition-colors">
              <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">
                {mskConfig.ClusterType || 'Unknown'}
              </div>
              <div className="text-sm text-gray-600 dark:text-gray-400">Cluster Type</div>
            </div>
            <div className="bg-gray-50 dark:bg-card rounded-lg p-4 transition-colors">
              <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">
                {provisioned.NumberOfBrokerNodes}
              </div>
              <div className="text-sm text-gray-600 dark:text-gray-400">Broker Nodes</div>
            </div>
            <div className="bg-gray-50 dark:bg-card rounded-lg p-4 transition-colors">
              <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">
                {provisioned.CurrentBrokerSoftwareInfo?.KafkaVersion || 'Unknown'}
              </div>
              <div className="text-sm text-gray-600 dark:text-gray-400">Kafka Version</div>
            </div>
          </div>
        </div>

        {/* All Tabs */}
        <Tabs
          tabs={[
            { id: 'cluster', label: 'Cluster' },
            { id: 'metrics', label: 'Metrics' },
            { id: 'topics', label: 'Topics' },
            { id: 'connectors', label: 'Connectors' },
            { id: 'acls', label: 'ACLs' },
          ]}
          activeId={activeTab}
          onChange={(id) => setActiveTab(id as any)}
          className="border-b border-gray-200 dark:border-border"
        />

        {/* Tab Content */}
        <div className="p-6">
          {/* Metrics Tab */}
          {activeTab === 'metrics' && (
            <div className="min-w-0 max-w-full">
              <ClusterMetrics
                cluster={{
                  name: cluster.name,
                  region: regionName,
                }}
                isActive={activeTab === 'metrics'}
              />
            </div>
          )}

          {/* Topics Tab */}
          {activeTab === 'topics' && (
            <div className="min-w-0 max-w-full">
              <ClusterTopics kafkaAdminInfo={cluster.kafka_admin_client_information} />
            </div>
          )}

          {/* Connectors Tab */}
          {activeTab === 'connectors' && (
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
          {activeTab === 'acls' && (
            <div className="min-w-0 max-w-full">
              <ClusterACLs acls={cluster.kafka_admin_client_information?.acls || []} />
            </div>
          )}

          {/* Cluster Configuration Tab */}
          {activeTab === 'cluster' && (
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
