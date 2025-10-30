import { useState } from 'react'
import ClusterMetrics from './ClusterMetrics'
import ClusterTopics from './ClusterTopics'
import ClusterConnectors from './ClusterConnectors'
import ClusterACLs from './ClusterACLs'

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
      bootstrap_brokers?: any
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

  const formatDate = (dateString: string) =>
    new Date(dateString).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })

  const getStatusBadge = (enabled: boolean, label: string) => (
    <span
      className={`text-sm font-medium ${
        enabled ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'
      }`}
    >
      {label}
    </span>
  )

  const decodeBase64 = (base64String: string) => {
    try {
      return atob(base64String)
    } catch {
      return 'Unable to decode'
    }
  }

  return (
    <div className="max-w-7xl mx-auto space-y-6 min-w-0 w-full">
      {/* Header */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 transition-colors">
        {/* Cluster Title and Key Metrics */}
        <div className="p-6 border-b border-gray-200 dark:border-gray-700">
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
            <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
              <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">
                {mskConfig.ClusterType || 'Unknown'}
              </div>
              <div className="text-sm text-gray-600 dark:text-gray-400">Cluster Type</div>
            </div>
            <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
              <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">
                {provisioned.NumberOfBrokerNodes}
              </div>
              <div className="text-sm text-gray-600 dark:text-gray-400">Broker Nodes</div>
            </div>
            <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
              <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">
                {provisioned.CurrentBrokerSoftwareInfo?.KafkaVersion || 'Unknown'}
              </div>
              <div className="text-sm text-gray-600 dark:text-gray-400">Kafka Version</div>
            </div>
          </div>
        </div>

        {/* All Tabs */}
        <div className="border-b border-gray-200 dark:border-gray-700">
          <nav className="-mb-px flex space-x-8 px-6 overflow-x-auto">
            {[
              { id: 'cluster', label: 'Cluster' },
              { id: 'metrics', label: 'Metrics' },
              { id: 'topics', label: 'Topics' },
              { id: 'connectors', label: 'Connectors' },
              { id: 'acls', label: 'ACLs' },
            ].map((tab) => (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id as any)}
                className={`py-4 px-1 border-b-2 font-medium text-sm transition-colors whitespace-nowrap ${
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
            <div className="space-y-8">
              {/* Basic Cluster Configuration */}
              <div>
                <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
                  Cluster Configuration
                </h3>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm">
                  <div className="flex justify-between">
                    <span className="text-gray-600 dark:text-gray-400">Instance Type:</span>
                    <span className="font-medium text-gray-900 dark:text-gray-100">
                      {brokerInfo.InstanceType || 'Unknown'}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-gray-600 dark:text-gray-400">
                      Storage per Broker (GB):
                    </span>
                    <span className="font-medium text-gray-900 dark:text-gray-100">
                      {brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-gray-600 dark:text-gray-400">Total Storage (GB):</span>
                    <span className="font-medium text-gray-900 dark:text-gray-100">
                      {(brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0) *
                        (provisioned.NumberOfBrokerNodes || 0)}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-gray-600 dark:text-gray-400">AZ Distribution:</span>
                    <span className="font-medium text-gray-900 dark:text-gray-100">
                      {brokerInfo.BrokerAZDistribution || 'Unknown'}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-gray-600 dark:text-gray-400">Availability Zones:</span>
                    <span className="font-medium text-gray-900 dark:text-gray-100">
                      {brokerInfo.ZoneIds?.length || 0}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-gray-600 dark:text-gray-400">Follower Fetching:</span>
                    <span className="font-medium">
                      {cluster.metrics?.metadata?.follower_fetching !== undefined ? (
                        <span
                          className={`${
                            cluster.metrics.metadata.follower_fetching
                              ? 'text-green-600 dark:text-green-400'
                              : 'text-red-600 dark:text-red-400'
                          }`}
                        >
                          {cluster.metrics.metadata.follower_fetching ? '✓' : '✗'}
                        </span>
                      ) : (
                        <span className="text-gray-500 dark:text-gray-400">Unknown</span>
                      )}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-gray-600 dark:text-gray-400">Tiered Storage:</span>
                    <span className="font-medium">
                      {cluster.metrics?.metadata?.tiered_storage !== undefined ? (
                        <span
                          className={`${
                            cluster.metrics.metadata.tiered_storage
                              ? 'text-green-600 dark:text-green-400'
                              : 'text-red-600 dark:text-red-400'
                          }`}
                        >
                          {cluster.metrics.metadata.tiered_storage ? '✓' : '✗'}
                        </span>
                      ) : (
                        <span className="text-gray-500 dark:text-gray-400">Unknown</span>
                      )}
                    </span>
                  </div>
                </div>
              </div>

              {/* Authentication Status */}
              <div>
                <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
                  Authentication Status
                </h3>
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-gray-200 dark:border-gray-600">
                        <th className="text-left py-2 font-medium text-gray-900 dark:text-gray-100">
                          Authentication Method
                        </th>
                        <th className="text-center py-2 font-medium text-gray-900 dark:text-gray-100">
                          Status
                        </th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-200 dark:divide-gray-600">
                      <tr>
                        <td className="py-2 text-gray-900 dark:text-gray-100">
                          IAM Authentication
                        </td>
                        <td className="py-2 text-center">
                          {getStatusBadge(
                            provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled || false,
                            provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled
                              ? 'Enabled'
                              : 'Disabled'
                          )}
                        </td>
                      </tr>
                      <tr>
                        <td className="py-2 text-gray-900 dark:text-gray-100">
                          SCRAM Authentication
                        </td>
                        <td className="py-2 text-center">
                          {getStatusBadge(
                            provisioned.ClientAuthentication?.Sasl?.Scram?.Enabled || false,
                            provisioned.ClientAuthentication?.Sasl?.Scram?.Enabled
                              ? 'Enabled'
                              : 'Disabled'
                          )}
                        </td>
                      </tr>
                      <tr>
                        <td className="py-2 text-gray-900 dark:text-gray-100">
                          TLS Authentication
                        </td>
                        <td className="py-2 text-center">
                          {getStatusBadge(
                            provisioned.ClientAuthentication?.Tls?.Enabled || false,
                            provisioned.ClientAuthentication?.Tls?.Enabled ? 'Enabled' : 'Disabled'
                          )}
                        </td>
                      </tr>
                      <tr>
                        <td className="py-2 text-gray-900 dark:text-gray-100">
                          Unauthenticated Access
                        </td>
                        <td className="py-2 text-center">
                          {getStatusBadge(
                            provisioned.ClientAuthentication?.Unauthenticated?.Enabled || false,
                            provisioned.ClientAuthentication?.Unauthenticated?.Enabled
                              ? 'Enabled'
                              : 'Disabled'
                          )}
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>

              {/* Bootstrap Brokers */}
              {cluster.aws_client_information?.bootstrap_brokers && (
                <div>
                  <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
                    Bootstrap Brokers
                  </h3>
                  {/* Public Access status (same styling as Monitoring section) */}
                  <div className="mb-4">
                    <div className="space-y-3">
                      <div className="flex items-center gap-2">
                        <span className="text-gray-600 dark:text-gray-400">Public Access:</span>
                        {getStatusBadge(
                          (mskConfig?.Provisioned?.BrokerNodeGroupInfo?.ConnectivityInfo?.PublicAccess?.Type || '') === 'SERVICE_PROVIDED_EIPS',
                          (mskConfig?.Provisioned?.BrokerNodeGroupInfo?.ConnectivityInfo?.PublicAccess?.Type || '') === 'SERVICE_PROVIDED_EIPS'
                            ? 'Enabled'
                            : 'Disabled'
                        )}
                      </div>
                    </div>
                  </div>
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b border-gray-200 dark:border-gray-600">
                          <th className="text-left py-2 font-medium text-gray-900 dark:text-gray-100">
                            Broker Type
                          </th>
                          <th className="text-left py-2 font-medium text-gray-900 dark:text-gray-100">
                            Addresses
                          </th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-200 dark:divide-gray-600">
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerString && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              Plaintext
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerString
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringTls && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              TLS
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringTls
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringPublicTls && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              Public TLS
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringPublicTls
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringSaslScram && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              SASL/SCRAM
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringSaslScram
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringPublicSaslScram && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              Public SASL/SCRAM
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringPublicSaslScram
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringSaslIam && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              SASL/IAM
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringSaslIam
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringPublicSaslIam && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              Public SASL/IAM
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringPublicSaslIam
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringVpcConnectivitySaslIam && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              VPC Connectivity SASL/IAM
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringVpcConnectivitySaslIam
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringVpcConnectivitySaslScram && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              VPC Connectivity SASL/SCRAM
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringVpcConnectivitySaslScram
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                        {cluster.aws_client_information.bootstrap_brokers
                          .BootstrapBrokerStringVpcConnectivityTls && (
                          <tr>
                            <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                              VPC Connectivity TLS
                            </td>
                            <td className="py-2">
                              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                                {
                                  cluster.aws_client_information.bootstrap_brokers
                                    .BootstrapBrokerStringVpcConnectivityTls
                                }
                              </span>
                            </td>
                          </tr>
                        )}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}

              {/* Network Configuration */}
              <div>
                <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
                  Network Configuration
                </h3>
                <div>
                  <h4 className="font-medium text-gray-900 dark:text-gray-100 mb-3">
                    Client Subnets
                  </h4>
                  <div className="space-y-2">
                    {(brokerInfo.ClientSubnets || []).map((subnet: string, index: number) => (
                      <div
                        key={index}
                        className="flex items-center justify-between p-2 bg-gray-50 dark:bg-gray-700 rounded transition-colors"
                      >
                        <span className="font-mono text-sm text-gray-900 dark:text-gray-100">
                          {subnet}
                        </span>
                        <span className="text-xs text-gray-500 dark:text-gray-400">
                          AZ: {brokerInfo.ZoneIds?.[index] || 'Unknown'}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              </div>

              {/* Storage Configuration */}
              <div>
                <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
                  Storage Configuration
                </h3>
                <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
                  <div className="space-y-2 text-sm">
                    <div className="flex justify-between">
                      <span className="text-gray-600 dark:text-gray-400">Volume Size:</span>
                      <span className="font-bold text-blue-600 dark:text-blue-400">
                        {brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0} GB
                      </span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-gray-600 dark:text-gray-400">Total Storage:</span>
                      <span className="font-bold text-green-600 dark:text-green-400">
                        {(brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0) *
                          (provisioned.NumberOfBrokerNodes || 0)}{' '}
                        GB
                      </span>
                    </div>
                    <div className="flex justify-between">
                      <span className="text-gray-600 dark:text-gray-400">
                        Provisioned Throughput:
                      </span>
                      {getStatusBadge(
                        brokerInfo.StorageInfo?.EbsStorageInfo?.ProvisionedThroughput?.Enabled ||
                          false,
                        brokerInfo.StorageInfo?.EbsStorageInfo?.ProvisionedThroughput?.Enabled
                          ? 'Enabled'
                          : 'Disabled'
                      )}
                    </div>
                  </div>
                </div>
              </div>

              {/* Security Configuration */}
              <div>
                <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
                  Security Configuration
                </h3>
                <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
                  {/* Authentication Methods */}
                  <div>
                    <h4 className="font-medium text-gray-900 dark:text-gray-100 mb-3">
                      Authentication Methods
                    </h4>
                    <div className="space-y-2">
                      <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors">
                        <span className="font-medium text-gray-900 dark:text-gray-100">
                          IAM Authentication
                        </span>
                        {getStatusBadge(
                          provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled || false,
                          provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled
                            ? 'Enabled'
                            : 'Disabled'
                        )}
                      </div>
                      <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors">
                        <span className="font-medium text-gray-900 dark:text-gray-100">
                          SCRAM Authentication
                        </span>
                        {getStatusBadge(
                          provisioned.ClientAuthentication?.Sasl?.Scram?.Enabled || false,
                          provisioned.ClientAuthentication?.Sasl?.Scram?.Enabled
                            ? 'Enabled'
                            : 'Disabled'
                        )}
                      </div>
                      <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors">
                        <span className="font-medium text-gray-900 dark:text-gray-100">
                          TLS Authentication
                        </span>
                        {getStatusBadge(
                          provisioned.ClientAuthentication?.Tls?.Enabled || false,
                          provisioned.ClientAuthentication?.Tls?.Enabled ? 'Enabled' : 'Disabled'
                        )}
                      </div>
                      <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors">
                        <span className="font-medium text-gray-900 dark:text-gray-100">
                          Unauthenticated Access
                        </span>
                        {getStatusBadge(
                          provisioned.ClientAuthentication?.Unauthenticated?.Enabled || false,
                          provisioned.ClientAuthentication?.Unauthenticated?.Enabled
                            ? 'Enabled'
                            : 'Disabled'
                        )}
                      </div>
                    </div>
                  </div>

                  {/* Encryption Settings */}
                  <div>
                    <h4 className="font-medium text-gray-900 dark:text-gray-100 mb-3">
                      Encryption Settings
                    </h4>
                    <div className="space-y-4">
                      <div>
                        <div className="text-sm font-medium text-gray-900 dark:text-gray-100 mb-2">
                          Encryption at Rest
                        </div>
                        <div className="bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors p-3">
                          <div className="text-sm text-gray-600 dark:text-gray-400">
                            KMS Key ID:
                          </div>
                          <div className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors mt-1">
                            {provisioned.EncryptionInfo?.EncryptionAtRest?.DataVolumeKMSKeyId?.split(
                              '/'
                            ).pop() || 'Not configured'}
                          </div>
                        </div>
                      </div>
                      <div>
                        <div className="text-sm font-medium text-gray-900 dark:text-gray-100 mb-2">
                          Encryption in Transit
                        </div>
                        <div className="space-y-2">
                          <div className="flex justify-between">
                            <span className="text-sm text-gray-600 dark:text-gray-400">
                              Client-Broker:
                            </span>
                            <span className="font-medium text-gray-900 dark:text-gray-100">
                              {provisioned.EncryptionInfo?.EncryptionInTransit?.ClientBroker ||
                                'Not configured'}
                            </span>
                          </div>
                          <div className="flex justify-between">
                            <span className="text-sm text-gray-600 dark:text-gray-400">
                              In-Cluster:
                            </span>
                            {getStatusBadge(
                              provisioned.EncryptionInfo?.EncryptionInTransit?.InCluster || false,
                              provisioned.EncryptionInfo?.EncryptionInTransit?.InCluster
                                ? 'Enabled'
                                : 'Disabled'
                            )}
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              {/* Monitoring & Logging */}
              <div>
                <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
                  Monitoring & Logging Configuration
                </h3>
                <div>
                  <h4 className="font-medium text-gray-900 dark:text-gray-100 mb-3">
                    Monitoring Configuration
                  </h4>
                  <div className="space-y-3">
                    <div className="flex justify-between items-center">
                      <span className="text-gray-600 dark:text-gray-400">Enhanced Monitoring:</span>
                      <span className="font-medium text-gray-900 dark:text-gray-100 bg-blue-100 dark:bg-blue-900 text-blue-800 dark:text-blue-200 px-2 py-1 rounded">
                        {provisioned.EnhancedMonitoring}
                      </span>
                    </div>
                    <div className="flex justify-between items-center">
                      <span className="text-gray-600 dark:text-gray-400">CloudWatch Logs:</span>
                      {getStatusBadge(
                        provisioned.LoggingInfo?.BrokerLogs?.CloudWatchLogs?.Enabled || false,
                        provisioned.LoggingInfo?.BrokerLogs?.CloudWatchLogs?.Enabled
                          ? 'Enabled'
                          : 'Disabled'
                      )}
                    </div>
                    <div className="flex justify-between items-center">
                      <span className="text-gray-600 dark:text-gray-400">Firehose Logs:</span>
                      {getStatusBadge(
                        provisioned.LoggingInfo?.BrokerLogs?.Firehose?.Enabled || false,
                        provisioned.LoggingInfo?.BrokerLogs?.Firehose?.Enabled
                          ? 'Enabled'
                          : 'Disabled'
                      )}
                    </div>
                    <div className="flex justify-between items-center">
                      <span className="text-gray-600 dark:text-gray-400">S3 Logs:</span>
                      {getStatusBadge(
                        provisioned.LoggingInfo?.BrokerLogs?.S3?.Enabled || false,
                        provisioned.LoggingInfo?.BrokerLogs?.S3?.Enabled ? 'Enabled' : 'Disabled'
                      )}
                    </div>
                  </div>
                </div>
              </div>

              {/* Broker Settings */}
              <div>
                <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
                  Broker Settings
                </h3>
                {(() => {
                  // Get the cluster's configuration ARN
                  const clusterConfigArn = provisioned?.CurrentBrokerSoftwareInfo?.ConfigurationArn

                  // Find the matching configuration in region configurations
                  const clusterConfig = regionData?.configurations?.find(
                    (config: any) => config.Arn === clusterConfigArn
                  )

                  if (clusterConfig) {
                    return (
                      <div className="space-y-6">
                        <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
                          <div className="flex items-center justify-between mb-4">
                            <div>
                              <div className="font-medium text-gray-900 dark:text-gray-100">
                                {clusterConfig.Arn.split('/').slice(-2, -1)[0]}
                              </div>
                              <div className="text-sm text-gray-500 dark:text-gray-400">
                                Revision {clusterConfig.Revision} • Created{' '}
                                {formatDate(clusterConfig.CreationTime)}
                              </div>
                            </div>
                            <div className="text-sm text-gray-600 dark:text-gray-400">
                              {clusterConfig.Description || 'No description'}
                            </div>
                          </div>

                          {/* Server Properties */}
                          {clusterConfig.ServerProperties && (
                            <div className="mt-4">
                              <h4 className="font-medium text-gray-900 dark:text-gray-100 mb-2">
                                Server Properties
                              </h4>
                              <div className="bg-white dark:bg-gray-600 rounded-lg p-3 transition-colors">
                                <pre className="text-xs text-gray-800 dark:text-gray-200 overflow-auto max-h-48 whitespace-pre-wrap font-mono">
                                  {decodeBase64(clusterConfig.ServerProperties)}
                                </pre>
                              </div>
                            </div>
                          )}
                        </div>
                      </div>
                    )
                  } else if (clusterConfigArn) {
                    return (
                      <p className="text-gray-500 dark:text-gray-400">
                        Configuration not found for ARN: {clusterConfigArn}
                      </p>
                    )
                  } else {
                    return (
                      <p className="text-gray-500 dark:text-gray-400">
                        No configuration ARN found for this cluster.
                      </p>
                    )
                  }
                })()}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
