interface ClusterOverviewProps {
  mskConfig: any
  provisioned: any
  brokerInfo: any
  regionName: string
  regionData?: any
}

export default function ClusterOverview({
  mskConfig,
  provisioned,
  brokerInfo,
  regionData,
}: ClusterOverviewProps) {
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
      className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium text-gray-900 dark:text-gray-100 ${
        enabled ? 'bg-green-100 text-green-800' : 'bg-red-100 text-red-800'
      }`}
    >
      {enabled ? '✅' : '❌'} {label}
    </span>
  )

  return (
    <div className="space-y-6">
      {/* Key Metrics */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <div className="bg-blue-50 dark:bg-blue-900 rounded-lg p-4 transition-colors">
          <div className="text-2xl font-bold text-blue-600 dark:text-blue-300">
            {provisioned.NumberOfBrokerNodes}
          </div>
          <div className="text-sm text-blue-800 dark:text-blue-200">Broker Nodes</div>
        </div>
        <div className="bg-green-50 dark:bg-green-900 rounded-lg p-4 transition-colors">
          <div className="text-2xl font-bold text-green-600 dark:text-green-300">
            {brokerInfo.InstanceType || 'Unknown'}
          </div>
          <div className="text-sm text-green-800 dark:text-green-200">Instance Type</div>
        </div>
        <div className="bg-purple-50 dark:bg-purple-900 rounded-lg p-4 transition-colors">
          <div className="text-2xl font-bold text-purple-600 dark:text-purple-300">
            {brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0}GB
          </div>
          <div className="text-sm text-purple-800 dark:text-purple-200">Storage per Broker</div>
        </div>
        <div className="bg-orange-50 dark:bg-orange-900 rounded-lg p-4 transition-colors">
          <div className="text-2xl font-bold text-orange-600 dark:text-orange-300">
            {provisioned.CurrentBrokerSoftwareInfo?.KafkaVersion || 'Unknown'}
          </div>
          <div className="text-sm text-orange-800 dark:text-orange-200">Kafka Version</div>
        </div>
      </div>

      {/* Configuration Details */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-3">
            Cluster Configuration
          </h3>
          <div className="space-y-2 text-sm">
            <div className="flex justify-between">
              <span className="text-gray-600 dark:text-gray-400 dark:text-gray-400">
                Cluster ARN:
              </span>
              <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                {mskConfig.ClusterArn.split('/').pop()}
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600 dark:text-gray-400 dark:text-gray-400">
                AZ Distribution:
              </span>
              <span className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100">
                {brokerInfo.BrokerAZDistribution || 'Unknown'}
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600 dark:text-gray-400 dark:text-gray-400">
                Availability Zones:
              </span>
              <span className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100">
                {brokerInfo.ZoneIds?.length || 0} zones
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600 dark:text-gray-400 dark:text-gray-400">Subnets:</span>
              <span className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100">
                {brokerInfo.ClientSubnets?.length || 0} subnets
              </span>
            </div>
          </div>
        </div>

        <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-3">
            Authentication Status
          </h3>
          <div className="space-y-2">
            {getStatusBadge(
              provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled || false,
              'IAM Authentication'
            )}
            {getStatusBadge(
              provisioned.ClientAuthentication?.Sasl?.Scram?.Enabled || false,
              'SCRAM Authentication'
            )}
            {getStatusBadge(
              provisioned.ClientAuthentication?.Tls?.Enabled || false,
              'TLS Authentication'
            )}
            {getStatusBadge(
              provisioned.ClientAuthentication?.Unauthenticated?.Enabled || false,
              'Unauthenticated Access'
            )}
          </div>
        </div>
      </div>

      {/* Network Configuration */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          🌐 Network Configuration
        </h3>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          <div>
            <h4 className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100 mb-3">
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
          <div>
            <h4 className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100 mb-3">
              Storage Configuration
            </h4>
            <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
              <div className="space-y-2 text-sm">
                <div className="flex justify-between">
                  <span className="text-gray-600 dark:text-gray-400 dark:text-gray-400">
                    Volume Size:
                  </span>
                  <span className="font-bold text-blue-600 dark:text-blue-400">
                    {brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0} GB
                  </span>
                </div>
                <div className="flex justify-between">
                  <span className="text-gray-600 dark:text-gray-400 dark:text-gray-400">
                    Total Storage:
                  </span>
                  <span className="font-bold text-green-600 dark:text-green-400">
                    {(brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0) *
                      (provisioned.NumberOfBrokerNodes || 0)}{' '}
                    GB
                  </span>
                </div>
                <div className="flex justify-between">
                  <span className="text-gray-600 dark:text-gray-400 dark:text-gray-400">
                    Provisioned Throughput:
                  </span>
                  {getStatusBadge(
                    brokerInfo.StorageInfo?.EbsStorageInfo?.ProvisionedThroughput?.Enabled || false,
                    'Enabled'
                  )}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Security Configuration */}
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          🔐 Security Configuration
        </h3>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Authentication Methods */}
          <div>
            <h4 className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100 mb-3">
              Authentication Methods
            </h4>
            <div className="space-y-2">
              <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors">
                <span className="font-medium text-gray-900 dark:text-gray-100">
                  IAM Authentication
                </span>
                {getStatusBadge(
                  provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled || false,
                  'IAM'
                )}
              </div>
              <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors">
                <span className="font-medium text-gray-900 dark:text-gray-100">
                  SCRAM Authentication
                </span>
                {getStatusBadge(
                  provisioned.ClientAuthentication?.Sasl?.Scram?.Enabled || false,
                  'SCRAM'
                )}
              </div>
              <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors">
                <span className="font-medium text-gray-900 dark:text-gray-100">
                  TLS Authentication
                </span>
                {getStatusBadge(provisioned.ClientAuthentication?.Tls?.Enabled || false, 'TLS')}
              </div>
              <div className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors">
                <span className="font-medium text-gray-900 dark:text-gray-100">
                  Unauthenticated Access
                </span>
                {getStatusBadge(
                  provisioned.ClientAuthentication?.Unauthenticated?.Enabled || false,
                  'Unauth'
                )}
              </div>
            </div>
          </div>

          {/* Encryption Settings */}
          <div>
            <h4 className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100 mb-3">
              Encryption Settings
            </h4>
            <div className="space-y-4">
              <div>
                <div className="text-sm font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100 mb-2">
                  Encryption at Rest
                </div>
                <div className="bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors p-3">
                  <div className="text-sm text-gray-600 dark:text-gray-400">KMS Key ID:</div>
                  <div className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors mt-1">
                    {provisioned.EncryptionInfo?.EncryptionAtRest?.DataVolumeKMSKeyId?.split(
                      '/'
                    ).pop() || 'Not configured'}
                  </div>
                </div>
              </div>
              <div>
                <div className="text-sm font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100 mb-2">
                  Encryption in Transit
                </div>
                <div className="space-y-2">
                  <div className="flex justify-between">
                    <span className="text-sm text-gray-600 dark:text-gray-400">Client-Broker:</span>
                    <span className="font-medium text-gray-900 dark:text-gray-100">
                      {provisioned.EncryptionInfo?.EncryptionInTransit?.ClientBroker ||
                        'Not configured'}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-sm text-gray-600 dark:text-gray-400">In-Cluster:</span>
                    {getStatusBadge(
                      provisioned.EncryptionInfo?.EncryptionInTransit?.InCluster || false,
                      'Enabled'
                    )}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Monitoring Configuration */}
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          📈 Monitoring & Logging Configuration
        </h3>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Monitoring Settings */}
          <div>
            <h4 className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100 mb-3">
              Monitoring Configuration
            </h4>
            <div className="space-y-3">
              <div className="flex justify-between items-center">
                <span className="text-gray-600 dark:text-gray-400">Enhanced Monitoring:</span>
                <span className="font-medium text-gray-900 dark:text-gray-100 bg-blue-100 text-blue-800 px-2 py-1 rounded">
                  {provisioned.EnhancedMonitoring}
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-gray-600 dark:text-gray-400">CloudWatch Logs:</span>
                {getStatusBadge(
                  provisioned.LoggingInfo?.BrokerLogs?.CloudWatchLogs?.Enabled || false,
                  'CloudWatch'
                )}
              </div>
              <div className="flex justify-between items-center">
                <span className="text-gray-600 dark:text-gray-400">Firehose Logs:</span>
                {getStatusBadge(
                  provisioned.LoggingInfo?.BrokerLogs?.Firehose?.Enabled || false,
                  'Firehose'
                )}
              </div>
              <div className="flex justify-between items-center">
                <span className="text-gray-600 dark:text-gray-400">S3 Logs:</span>
                {getStatusBadge(provisioned.LoggingInfo?.BrokerLogs?.S3?.Enabled || false, 'S3')}
              </div>
            </div>
          </div>

          {/* Broker Configuration */}
          <div>
            <h4 className="font-medium text-gray-900 dark:text-gray-100 text-gray-900 dark:text-gray-100 mb-3">
              Broker Configuration
            </h4>
            <div className="space-y-3">
              <div className="flex justify-between">
                <span className="text-gray-600 dark:text-gray-400">Configuration ARN:</span>
                <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                  {provisioned.CurrentBrokerSoftwareInfo?.ConfigurationArn?.split('/').pop() ||
                    'Not configured'}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-600 dark:text-gray-400">Configuration Revision:</span>
                <span className="font-medium text-gray-900 dark:text-gray-100">
                  {provisioned.CurrentBrokerSoftwareInfo?.ConfigurationRevision || 'Unknown'}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-600 dark:text-gray-400">Customer Action Status:</span>
                <span className="font-medium text-gray-900 dark:text-gray-100 bg-gray-100 px-2 py-1 rounded">
                  {(provisioned as any).CustomerActionStatus || 'NONE'}
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Available Configurations */}
      {regionData?.configurations && regionData.configurations.length > 0 && (
        <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
          <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
            📋 Available Configurations
          </h3>
          <div className="space-y-3">
            {regionData.configurations.slice(0, 5).map((config: any, index: number) => (
              <div
                key={index}
                className="flex items-center justify-between p-3 bg-gray-50 dark:bg-gray-700 rounded-lg transition-colors"
              >
                <div>
                  <div className="font-medium text-gray-900 dark:text-gray-100 text-gray-900">
                    {config.Arn.split('/').slice(-2, -1)[0]}
                  </div>
                  <div className="text-sm text-gray-500">
                    Revision {config.Revision} • Created {formatDate(config.CreationTime)}
                  </div>
                </div>
                <div className="text-sm text-gray-600 dark:text-gray-400">
                  {config.Description || 'No description'}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
