import { useState } from 'react'
import ClusterOverview from './ClusterOverview'
import ClusterMetrics from './ClusterMetrics'
import ClusterTopics from './ClusterTopics'

interface ClusterReportProps {
  cluster: {
    name: string
    metrics?: {
      metadata: {
        cluster_type: string
        follower_fetching: boolean
        broker_az_distribution: string
        kafka_version: string
        enhanced_monitoring: string
        start_window_date: string
        end_window_date: string
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
      msk_cluster_config: any
    }
    kafka_admin_client_information?: any
  }
  regionName: string
  regionData?: any
}

export default function ClusterReport({ cluster, regionName, regionData }: ClusterReportProps) {
  const [activeTab, setActiveTab] = useState<'overview' | 'topics' | 'metrics'>('overview')

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

  const formatDate = (dateString: string) =>
    new Date(dateString).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })

  return (
    <div className="max-w-7xl mx-auto space-y-6 min-w-0 w-full">
      {/* Header */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold text-gray-900 dark:text-gray-100">
              Cluster:&nbsp;{cluster.name}
            </h1>
            <p className="text-lg text-gray-600 dark:text-gray-300 mt-1">
              {regionName} • {mskConfig.ClusterType || 'Unknown'} •{' '}
              {provisioned.NumberOfBrokerNodes || 0} brokers
            </p>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
              Created: {mskConfig.CreationTime ? formatDate(mskConfig.CreationTime) : 'Unknown'} •
              Version: {mskConfig.CurrentVersion || 'Unknown'}
            </p>
          </div>
        </div>
      </div>

      {/* Tab Navigation */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 transition-colors min-w-0 max-w-full overflow-hidden">
        <div className="border-b border-gray-200 dark:border-gray-700">
          <nav className="-mb-px flex space-x-8 px-6">
            {[
              { id: 'overview', label: 'Overview', icon: '' },
              { id: 'metrics', label: 'Metrics', icon: '' },
              { id: 'topics', label: 'Topics', icon: '' },
            ].map((tab) => (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id as any)}
                className={`py-4 px-1 border-b-2 font-medium text-sm transition-colors ${
                  activeTab === tab.id
                    ? 'border-blue-500 text-blue-600 dark:text-blue-400'
                    : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-gray-300 dark:hover:border-gray-600'
                }`}
              >
                {tab.label}
              </button>
            ))}
          </nav>
        </div>

        <div className="p-6 min-w-0 max-w-full overflow-hidden">
          {/* Overview Tab */}
          {activeTab === 'overview' && (
            <div className="min-w-0 max-w-full">
              <ClusterOverview
                mskConfig={mskConfig}
                provisioned={provisioned}
                brokerInfo={brokerInfo}
                regionName={regionName}
                regionData={regionData}
              />
            </div>
          )}

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
        </div>
      </div>
    </div>
  )
}
