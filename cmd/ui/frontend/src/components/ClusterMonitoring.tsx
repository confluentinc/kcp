interface ClusterMonitoringProps {
  provisioned: any
  regionData?: any
}

export default function ClusterMonitoring({ provisioned, regionData }: ClusterMonitoringProps) {
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
      className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
        enabled ? 'bg-green-100 text-green-800' : 'bg-red-100 text-red-800'
      }`}
    >
      {enabled ? '✅' : '❌'} {label}
    </span>
  )

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div className="bg-white rounded-lg border p-6">
          <h3 className="text-xl font-semibold text-gray-900 mb-4">📈 Monitoring Configuration</h3>
          <div className="space-y-3">
            <div className="flex justify-between items-center">
              <span className="text-gray-600">Enhanced Monitoring:</span>
              <span className="font-medium bg-blue-100 text-blue-800 px-2 py-1 rounded">
                {provisioned.EnhancedMonitoring}
              </span>
            </div>
            <div className="flex justify-between items-center">
              <span className="text-gray-600">CloudWatch Logs:</span>
              {getStatusBadge(
                provisioned.LoggingInfo?.BrokerLogs?.CloudWatchLogs?.Enabled || false,
                'CloudWatch'
              )}
            </div>
            <div className="flex justify-between items-center">
              <span className="text-gray-600">Firehose Logs:</span>
              {getStatusBadge(
                provisioned.LoggingInfo?.BrokerLogs?.Firehose?.Enabled || false,
                'Firehose'
              )}
            </div>
            <div className="flex justify-between items-center">
              <span className="text-gray-600">S3 Logs:</span>
              {getStatusBadge(provisioned.LoggingInfo?.BrokerLogs?.S3?.Enabled || false, 'S3')}
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg border p-6">
          <h3 className="text-xl font-semibold text-gray-900 mb-4">⚙️ Broker Configuration</h3>
          <div className="space-y-3">
            <div className="flex justify-between">
              <span className="text-gray-600">Configuration ARN:</span>
              <span className="font-mono text-xs bg-gray-200 px-2 py-1 rounded">
                {provisioned.CurrentBrokerSoftwareInfo?.ConfigurationArn?.split('/').pop() ||
                  'Not configured'}
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600">Configuration Revision:</span>
              <span className="font-medium">
                {provisioned.CurrentBrokerSoftwareInfo?.ConfigurationRevision || 'Unknown'}
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600">Customer Action Status:</span>
              <span className="font-medium bg-gray-100 px-2 py-1 rounded">
                {(provisioned as any).CustomerActionStatus || 'NONE'}
              </span>
            </div>
          </div>
        </div>
      </div>

      {/* Configurations List */}
      {regionData?.configurations && regionData.configurations.length > 0 && (
        <div className="bg-white rounded-lg border p-6">
          <h3 className="text-xl font-semibold text-gray-900 mb-4">📋 Available Configurations</h3>
          <div className="space-y-3">
            {regionData.configurations.slice(0, 5).map((config: any, index: number) => (
              <div
                key={index}
                className="flex items-center justify-between p-3 bg-gray-50 rounded-lg"
              >
                <div>
                  <div className="font-medium text-gray-900">
                    {config.Arn.split('/').slice(-2, -1)[0]}
                  </div>
                  <div className="text-sm text-gray-500">
                    Revision {config.Revision} • Created {formatDate(config.CreationTime)}
                  </div>
                </div>
                <div className="text-sm text-gray-600">
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
