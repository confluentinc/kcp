interface ClusterOverviewProps {
  mskConfig: any
  provisioned: any
  brokerInfo: any
  regionName: string
  regionData?: any
  bootstrapBrokers?: any
}

export default function ClusterOverview({
  mskConfig,
  provisioned,
  brokerInfo,
  regionData,
  bootstrapBrokers,
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
      className={`text-sm font-medium ${
        enabled ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'
      }`}
    >
      {label}
    </span>
  )

  return (
    <div className="space-y-6">
      {/* Key Metrics */}
      <div className="grid grid-cols-1 md:grid-cols-5 gap-4">
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
            {brokerInfo.InstanceType || 'Unknown'}
          </div>
          <div className="text-sm text-gray-600 dark:text-gray-400">Instance Type</div>
        </div>
        <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
          <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">
            {brokerInfo.StorageInfo?.EbsStorageInfo?.VolumeSize || 0}GB
          </div>
          <div className="text-sm text-gray-600 dark:text-gray-400">Storage per Broker</div>
        </div>
        <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
          <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">
            {provisioned.CurrentBrokerSoftwareInfo?.KafkaVersion || 'Unknown'}
          </div>
          <div className="text-sm text-gray-600 dark:text-gray-400">Kafka Version</div>
        </div>
      </div>
      
      {/* Bootstrap Brokers */}
      {bootstrapBrokers && (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-6 transition-colors">
          <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
            Bootstrap Brokers
          </h3>
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
                {bootstrapBrokers.BootstrapBrokerString && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">Plaintext</td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerString}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringTls && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">TLS</td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringTls}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringPublicTls && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">Public TLS</td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringPublicTls}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringSaslScram && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">SASL/SCRAM</td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringSaslScram}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringPublicSaslScram && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                      Public SASL/SCRAM
                    </td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringPublicSaslScram}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringSaslIam && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">SASL/IAM</td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringSaslIam}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringPublicSaslIam && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                      Public SASL/IAM
                    </td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringPublicSaslIam}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringVpcConnectivitySaslIam && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                      VPC Connectivity SASL/IAM
                    </td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringVpcConnectivitySaslIam}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringVpcConnectivitySaslScram && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                      VPC Connectivity SASL/SCRAM
                    </td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringVpcConnectivitySaslScram}
                      </span>
                    </td>
                  </tr>
                )}
                {bootstrapBrokers.BootstrapBrokerStringVpcConnectivityTls && (
                  <tr>
                    <td className="py-2 font-medium text-gray-900 dark:text-gray-100">
                      VPC Connectivity TLS
                    </td>
                    <td className="py-2">
                      <span className="font-mono text-xs bg-gray-200 dark:bg-gray-600 text-gray-900 dark:text-gray-100 px-2 py-1 rounded transition-colors">
                        {bootstrapBrokers.BootstrapBrokerStringVpcConnectivityTls}
                      </span>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

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
                  <td className="py-2 text-gray-900 dark:text-gray-100">IAM Authentication</td>
                  <td className="py-2 text-center">
                    {getStatusBadge(
                      provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled || false,
                      provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled ? 'Enabled' : 'Disabled'
                    )}
                  </td>
                </tr>
                <tr>
                  <td className="py-2 text-gray-900 dark:text-gray-100">SCRAM Authentication</td>
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
                  <td className="py-2 text-gray-900 dark:text-gray-100">TLS Authentication</td>
                  <td className="py-2 text-center">
                    {getStatusBadge(
                      provisioned.ClientAuthentication?.Tls?.Enabled || false,
                      provisioned.ClientAuthentication?.Tls?.Enabled ? 'Enabled' : 'Disabled'
                    )}
                  </td>
                </tr>
                <tr>
                  <td className="py-2 text-gray-900 dark:text-gray-100">Unauthenticated Access</td>
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
      </div>

      {/* Network Configuration */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          Network Configuration
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
          Security Configuration
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
          Monitoring & Logging Configuration
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
            Available Configurations
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
