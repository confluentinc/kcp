import KeyValueGrid from '@/components/common/KeyValueGrid'
import KeyValuePair from '@/components/common/KeyValuePair'
import BooleanStatus from '@/components/common/BooleanStatus'
import AuthenticationStatus from './AuthenticationStatus'
import StorageInfo from './StorageInfo'
import StatusBadge from '@/components/common/StatusBadge'
import { createStatusBadgeProps } from '@/lib/utils'
import { formatDate } from '@/lib/formatters'
import { decodeBase64 } from '@/lib/clusterUtils'

interface ClusterConfigurationSectionProps {
  cluster: {
    metrics?: {
      metadata: {
        follower_fetching: boolean
        tiered_storage: boolean
      }
    }
  }
  provisioned: any
  brokerInfo: any
  regionData?: any
}

export default function ClusterConfigurationSection({
  cluster,
  provisioned,
  brokerInfo,
  regionData,
}: ClusterConfigurationSectionProps) {
  return (
    <div className="space-y-8">
      {/* Basic Cluster Configuration */}
      <div>
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
          Cluster Configuration
        </h3>
        <KeyValueGrid
          items={[
            {
              label: 'Instance Type:',
              value: brokerInfo.InstanceType || 'Unknown',
            },
            {
              label: 'Storage per Broker (GB):',
              value: brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0,
            },
            {
              label: 'Total Storage (GB):',
              value:
                (brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0) *
                (provisioned.NumberOfBrokerNodes || 0),
            },
            {
              label: 'AZ Distribution:',
              value: brokerInfo.BrokerAZDistribution || 'Unknown',
            },
            {
              label: 'Availability Zones:',
              value: brokerInfo.ZoneIds?.length || 0,
            },
            {
              label: 'Follower Fetching:',
              value: <BooleanStatus value={cluster.metrics?.metadata?.follower_fetching} />,
            },
            {
              label: 'Tiered Storage:',
              value: <BooleanStatus value={cluster.metrics?.metadata?.tiered_storage} />,
            },
          ]}
        />
      </div>

      {/* Authentication Status */}
      <div>
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
          Authentication Status
        </h3>
        <AuthenticationStatus
          clientAuthentication={provisioned.ClientAuthentication || {}}
          displayMode="table"
        />
      </div>

      {/* Network Configuration */}
      <div>
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">
          Network Configuration
        </h3>
        <div>
          <h4 className="font-medium text-gray-900 dark:text-gray-100 mb-3">Client Subnets</h4>
          <div className="space-y-2">
            {(brokerInfo.ClientSubnets || []).map((subnet: string, index: number) => (
              <div
                key={index}
                className="flex items-center justify-between p-2 bg-gray-50 dark:bg-card rounded transition-colors"
              >
                <span className="font-mono text-sm text-gray-900 dark:text-gray-100">{subnet}</span>
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
        <StorageInfo
          volumeSize={brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0}
          brokerNodes={provisioned.NumberOfBrokerNodes || 0}
          provisionedThroughput={brokerInfo.StorageInfo?.EbsStorageInfo?.ProvisionedThroughput}
          displayMode="detailed"
        />
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
            <AuthenticationStatus
              clientAuthentication={provisioned.ClientAuthentication || {}}
              displayMode="list"
            />
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
                <div className="bg-gray-50 dark:bg-card rounded-lg transition-colors p-3">
                  <div className="text-sm text-gray-600 dark:text-gray-400">KMS Key ID:</div>
                  <div className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors mt-1">
                    {provisioned.EncryptionInfo?.EncryptionAtRest?.DataVolumeKMSKeyId?.split('/').pop() ||
                      'Not configured'}
                  </div>
                </div>
              </div>
              <div>
                <div className="text-sm font-medium text-gray-900 dark:text-gray-100 mb-2">
                  Encryption in Transit
                </div>
                <div className="space-y-2">
                  <KeyValuePair
                    label="Client-Broker:"
                    value={
                      provisioned.EncryptionInfo?.EncryptionInTransit?.ClientBroker ||
                      'Not configured'
                    }
                    labelClassName="text-sm text-gray-600 dark:text-gray-400"
                  />
                  <KeyValuePair
                    label="In-Cluster:"
                    value={
                      <StatusBadge
                        {...createStatusBadgeProps(
                          provisioned.EncryptionInfo?.EncryptionInTransit?.InCluster || false
                        )}
                      />
                    }
                    labelClassName="text-sm text-gray-600 dark:text-gray-400"
                  />
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
            <KeyValuePair
              label="Enhanced Monitoring:"
              value={
                <span className="font-medium text-gray-900 dark:text-gray-100 bg-blue-100 dark:bg-accent/20 text-blue-800 dark:text-accent px-2 py-1 rounded">
                  {provisioned.EnhancedMonitoring}
                </span>
              }
              alignItems="center"
            />
            <KeyValuePair
              label="CloudWatch Logs:"
              value={
                <StatusBadge
                  {...createStatusBadgeProps(
                    provisioned.LoggingInfo?.BrokerLogs?.CloudWatchLogs?.Enabled || false
                  )}
                />
              }
              alignItems="center"
            />
            <KeyValuePair
              label="Firehose Logs:"
              value={
                <StatusBadge
                  {...createStatusBadgeProps(
                    provisioned.LoggingInfo?.BrokerLogs?.Firehose?.Enabled || false
                  )}
                />
              }
              alignItems="center"
            />
            <KeyValuePair
              label="S3 Logs:"
              value={
                <StatusBadge
                  {...createStatusBadgeProps(provisioned.LoggingInfo?.BrokerLogs?.S3?.Enabled || false)}
                />
              }
              alignItems="center"
            />
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
                <div className="bg-gray-50 dark:bg-card rounded-lg p-4 transition-colors">
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
  )
}

