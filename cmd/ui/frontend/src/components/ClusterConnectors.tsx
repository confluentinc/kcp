interface Connector {
  connector_arn: string
  connector_name: string
  connector_state: string
  creation_time: string
  kafka_cluster: {
    BootstrapServers: string
    Vpc: {
      SecurityGroups: string[]
      Subnets: string[]
    }
  }
  kafka_cluster_client_authentication: {
    AuthenticationType: string
  }
  capacity: {
    AutoScaling?: {
      MaxWorkerCount: number
      McuCount: number
      MinWorkerCount: number
      ScaleInPolicy: { CpuUtilizationPercentage: number }
      ScaleOutPolicy: { CpuUtilizationPercentage: number }
    }
    ProvisionedCapacity?: any
  }
  plugins: Array<{
    CustomPlugin: {
      CustomPluginArn: string
      Revision: number
    }
  }>
  connector_configuration: Record<string, string>
}

interface ClusterConnectorsProps {
  connectors: Connector[]
}

export default function ClusterConnectors({ connectors }: ClusterConnectorsProps) {
  const formatDate = (dateString: string) =>
    new Date(dateString).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })

  if (!connectors || connectors.length === 0) {
    return (
      <div className="text-center py-12">
        <div className="text-gray-500 dark:text-gray-400 text-lg">No connectors found</div>
        <p className="text-sm text-gray-400 dark:text-gray-500 mt-2">
          This cluster doesn't have any Kafka Connect connectors configured.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Kafka Connect Connectors ({connectors.length})
        </h3>
      </div>

      <div className="grid gap-6">
        {connectors.map((connector) => (
          <div
            key={connector.connector_arn}
            className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg shadow-sm transition-colors"
          >
            {/* Connector Header */}
            <div className="p-6 border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-700">
              <div className="flex items-start justify-between">
                <div className="flex-1">
                  <div className="flex items-center gap-3 mb-2">
                    <h4 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
                      {connector.connector_name}
                    </h4>
                  </div>
                  <p className="text-sm text-gray-500 dark:text-gray-400">
                    Created: {formatDate(connector.creation_time)}
                  </p>
                  <p className="text-xs text-gray-400 dark:text-gray-500 font-mono mt-1">
                    {connector.connector_arn}
                  </p>
                </div>
              </div>
            </div>

            {/* Connector Details */}
            <div className="p-6 space-y-6">
              {/* Basic Information - All in one row */}
              <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 divide-x divide-gray-200 dark:divide-gray-600">
                <div className="lg:pr-6">
                  <h5 className="font-medium text-gray-900 dark:text-gray-100 mb-3">
                    Authentication
                  </h5>
                  <div className="space-y-2 text-sm">
                    <div className="flex justify-between">
                      <span className="text-gray-600 dark:text-gray-400">Type:</span>
                      <span className="font-medium text-gray-900 dark:text-gray-100">
                        {connector.kafka_cluster_client_authentication.AuthenticationType}
                      </span>
                    </div>
                  </div>
                </div>

                <div className="lg:px-6">
                  <h5 className="font-medium text-gray-900 dark:text-gray-100 mb-3">Capacity</h5>
                  <div className="space-y-2 text-sm">
                    {connector.capacity.AutoScaling ? (
                      <>
                        <div className="flex justify-between">
                          <span className="text-gray-600 dark:text-gray-400">Min Workers:</span>
                          <span className="font-medium text-gray-900 dark:text-gray-100">
                            {connector.capacity.AutoScaling.MinWorkerCount}
                          </span>
                        </div>
                        <div className="flex justify-between">
                          <span className="text-gray-600 dark:text-gray-400">Max Workers:</span>
                          <span className="font-medium text-gray-900 dark:text-gray-100">
                            {connector.capacity.AutoScaling.MaxWorkerCount}
                          </span>
                        </div>
                        <div className="flex justify-between">
                          <span className="text-gray-600 dark:text-gray-400">MCU Count:</span>
                          <span className="font-medium text-gray-900 dark:text-gray-100">
                            {connector.capacity.AutoScaling.McuCount}
                          </span>
                        </div>
                      </>
                    ) : (
                      <div className="text-gray-500 dark:text-gray-400">
                        Provisioned capacity configuration
                      </div>
                    )}
                  </div>
                </div>

                {/* Auto Scaling Policies */}
                {connector.capacity.AutoScaling && (
                  <div className="lg:pl-6">
                    <h5 className="font-medium text-gray-900 dark:text-gray-100 mb-3">
                      Auto Scaling Policies
                    </h5>
                    <div className="space-y-2">
                      <div className="text-sm text-gray-900 dark:text-gray-100">
                        <span className="font-medium">Scale Out Policy</span> CPU ≥{' '}
                        {connector.capacity.AutoScaling.ScaleOutPolicy.CpuUtilizationPercentage}%
                      </div>
                      <div className="text-sm text-gray-900 dark:text-gray-100">
                        <span className="font-medium">Scale In Policy</span> CPU ≤{' '}
                        {connector.capacity.AutoScaling.ScaleInPolicy.CpuUtilizationPercentage}%
                      </div>
                    </div>
                  </div>
                )}
              </div>

              {/* Connector Configuration */}
              <div>
                <div className="mb-3">
                  <h5 className="font-medium text-gray-900 dark:text-gray-100">
                    Connector Configuration
                  </h5>
                </div>

                <div className="overflow-x-auto">
                  <table className="w-full border border-gray-200 dark:border-gray-600 rounded-lg">
                    <thead>
                      <tr className="bg-gray-50 dark:bg-gray-700">
                        <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-gray-200 dark:border-gray-600">
                          Property
                        </th>
                        <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-l border-gray-200 dark:border-gray-600">
                          Value
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {Object.entries(connector.connector_configuration).map(([key, value]) => (
                        <tr
                          key={key}
                          className="hover:bg-gray-50 dark:hover:bg-gray-700 transition-colors"
                        >
                          <td className="px-4 py-3 text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-gray-200 dark:border-gray-600">
                            {key}
                          </td>
                          <td className="px-4 py-3 text-sm text-gray-600 dark:text-gray-400 font-mono break-all border-b border-l border-gray-200 dark:border-gray-600">
                            {value}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
