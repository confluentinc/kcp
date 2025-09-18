import { Button } from '@/components/ui/button'
import { useState, useMemo } from 'react'
import ClusterOverview from './ClusterOverview'
import ClusterMetrics from './ClusterMetrics'

interface ClusterReportProps {
  cluster: {
    name: string
    metrics?: {
      broker_az_distribution: string
      kafka_version: string
      enhanced_monitoring: string
      start_window_date: string
      end_window_date: string
      buckets: Array<{
        start: string
        end: string
        data: {
          bytes_in_per_sec_avg: number
          bytes_out_per_sec_avg: number
          messages_in_per_sec_avg: number
        }
      }>
    }
    aws_client_information: {
      msk_cluster_config: any
    }
    kafk_admin_client_information?: any
  }
  regionName: string
  regionData?: any
}

export default function ClusterReport({ cluster, regionName, regionData }: ClusterReportProps) {
  const [activeTab, setActiveTab] = useState<'overview' | 'metrics'>('overview')

  // MSK-only cost calculation for header - must be before early return
  const mskCost = useMemo(() => {
    const costBuckets = regionData?.costs?.buckets || []
    return costBuckets.reduce((sum: number, bucket: any) => {
      const mskService = bucket.data?.['Amazon Managed Streaming for Apache Kafka'] || {}
      const mskTotal = Object.values(mskService).reduce(
        (serviceSum: number, cost: any) => serviceSum + (typeof cost === 'number' ? cost : 0),
        0
      )
      return sum + mskTotal
    }, 0)
  }, [regionData?.costs?.buckets])

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

  const formatCurrency = (amount: number) =>
    new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(amount)

  const formatDate = (dateString: string) =>
    new Date(dateString).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })

  return (
    <div className="max-w-7xl mx-auto space-y-6">
      {/* Page Title */}
      <div className="mb-6">
        <h1 className="text-3xl font-bold text-gray-900 dark:text-gray-100">Cluster Summary</h1>
        <p className="text-gray-600 dark:text-gray-400 mt-2">
          Configuration details and performance metrics for the selected cluster
        </p>
      </div>

      {/* Header */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold text-gray-900 dark:text-gray-100">{cluster.name}</h1>
            <p className="text-lg text-gray-600 dark:text-gray-300 mt-1">
              {regionName} • {mskConfig.ClusterType || 'Unknown'} •{' '}
              {provisioned.NumberOfBrokerNodes || 0} brokers
            </p>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
              Created: {mskConfig.CreationTime ? formatDate(mskConfig.CreationTime) : 'Unknown'} •
              Version: {mskConfig.CurrentVersion || 'Unknown'}
            </p>
          </div>
          <div className="text-right">
            <div className="text-2xl font-bold text-blue-600 dark:text-blue-400">
              {formatCurrency(mskCost)}
            </div>
            <p className="text-sm text-gray-500 dark:text-gray-400">MSK Service Cost</p>
          </div>
        </div>
      </div>

      {/* Tab Navigation */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 transition-colors">
        <div className="border-b border-gray-200 dark:border-gray-700">
          <nav className="-mb-px flex space-x-8 px-6">
            {[
              { id: 'overview', label: 'Overview', icon: '📊' },
              { id: 'metrics', label: 'Metrics', icon: '⚡' },
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
                {tab.icon} {tab.label}
              </button>
            ))}
          </nav>
        </div>

        <div className="p-6">
          {/* Overview Tab */}
          {activeTab === 'overview' && (
            <ClusterOverview
              mskConfig={mskConfig}
              provisioned={provisioned}
              brokerInfo={brokerInfo}
              regionName={regionName}
              regionData={regionData}
            />
          )}

          {/* Metrics Tab */}
          {activeTab === 'metrics' && cluster.metrics && (
            <ClusterMetrics cluster={cluster as any} />
          )}
          {activeTab === 'metrics' && !cluster.metrics && (
            <div className="bg-white rounded-lg border p-6">
              <h3 className="text-xl font-semibold text-gray-900 mb-4">⚡ Cluster Metrics</h3>
              <p className="text-gray-500">No metrics data available for this cluster.</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
