import { useState } from 'react'
import { Button } from '@/components/common/ui/button'
import { formatDate } from '@/lib/formatters'
import { CONNECTOR_TABS } from '@/constants'
import type { ConnectorTab } from '@/types'

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
    ProvisionedCapacity?: {
      WorkerCount?: number
      McuCount?: number
    }
  }
  plugins: Array<{
    CustomPlugin: {
      CustomPluginArn: string
      Revision: number
    }
  }>
  connector_configuration: Record<string, string>
}

interface SelfManagedConnector {
  name: string
  config: Record<string, string>
  state: string
  connect_host: string
}

interface ClusterConnectorsProps {
  connectors: Connector[]
  selfManagedConnectors?: SelfManagedConnector[]
}

export default function ClusterConnectors({
  connectors,
  selfManagedConnectors = [],
}: ClusterConnectorsProps) {
  const [activeTab, setActiveTab] = useState<ConnectorTab>(CONNECTOR_TABS.MSK)

  const renderSelfManagedConnector = (connector: SelfManagedConnector) => (
    <div
      key={connector.name}
      className="bg-white dark:bg-card border border-gray-200 dark:border-border rounded-lg shadow-sm transition-colors"
    >
      {/* Connector Header */}
      <div className="p-6 border-b border-gray-200 dark:border-border bg-gray-50 dark:bg-card">
        <div className="flex items-start justify-between">
          <div className="flex-1">
            <div className="flex items-center gap-3 mb-2">
              <h4 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
                {connector.name}
              </h4>
            </div>
          </div>
        </div>
      </div>

      {/* Connector Configuration */}
      <div className="p-6">
        <div className="flex items-center justify-between mb-3">
          <h5 className="font-medium text-gray-900 dark:text-gray-100">Connector Configuration</h5>
          <Button
            onClick={() => {
              const configText = Object.entries(connector.config)
                .map(([key, value]) => `${key}=${value}`)
                .join('\n')
              navigator.clipboard.writeText(configText)
            }}
            variant="outline"
            size="sm"
          >
            Copy Config
          </Button>
        </div>

        <textarea
          readOnly
          value={Object.entries(connector.config)
            .map(([key, value]) => `${key}=${value}`)
            .join('\n')}
          className="w-full h-48 p-3 text-sm font-mono bg-gray-50 dark:bg-card border border-gray-200 dark:border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 dark:text-gray-100"
        />
      </div>
    </div>
  )

  const renderMSKConnectors = () => {
    if (!connectors || connectors.length === 0) {
      return (
        <div className="text-center py-12">
          <div className="text-gray-500 dark:text-gray-400 text-lg">No MSK connectors found</div>
          <p className="text-sm text-gray-400 dark:text-gray-500 mt-2">
            This cluster doesn't have any MSK Connect connectors configured.
          </p>
        </div>
      )
    }

    return (
      <div className="grid gap-6">
        {connectors.map((connector) => (
          <div
            key={connector.connector_arn}
            className="bg-white dark:bg-card border border-gray-200 dark:border-border rounded-lg shadow-sm transition-colors"
          >
            {/* Connector Header */}
            <div className="p-6 border-b border-gray-200 dark:border-border bg-gray-50 dark:bg-card">
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
                <div className="flex items-center justify-between mb-3">
                  <h5 className="font-medium text-gray-900 dark:text-gray-100">
                    Connector Configuration
                  </h5>
                  <Button
                    onClick={() => {
                      const configText = Object.entries(connector.connector_configuration)
                        .map(([key, value]) => `${key}=${value}`)
                        .join('\n')
                      navigator.clipboard.writeText(configText)
                    }}
                    variant="outline"
                    size="sm"
                  >
                    Copy Config
                  </Button>
                </div>

                <textarea
                  readOnly
                  value={Object.entries(connector.connector_configuration)
                    .map(([key, value]) => `${key}=${value}`)
                    .join('\n')}
                  className="w-full h-48 p-3 text-sm font-mono bg-gray-50 dark:bg-card border border-gray-200 dark:border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 dark:text-gray-100"
                />
              </div>
            </div>
          </div>
        ))}
      </div>
    )
  }

  const renderSelfManagedConnectors = () => {
    if (!selfManagedConnectors || selfManagedConnectors.length === 0) {
      return (
        <div className="text-center py-12">
          <div className="text-gray-500 dark:text-gray-400 text-lg">
            No self-managed connectors found
          </div>
          <p className="text-sm text-gray-400 dark:text-gray-500 mt-2">
            This cluster doesn't have any self-managed Kafka Connect connectors configured.
          </p>
        </div>
      )
    }

    // Group connectors by connect_host
    const groupedConnectors = selfManagedConnectors.reduce((groups, connector) => {
      const host = connector.connect_host
      if (!groups[host]) {
        groups[host] = []
      }
      groups[host].push(connector)
      return groups
    }, {} as Record<string, SelfManagedConnector[]>)

    return (
      <div className="space-y-8">
        {Object.entries(groupedConnectors).map(([connectHost, connectors]) => (
          <div
            key={connectHost}
            className="space-y-4"
          >
            <div className="border-b border-gray-200 dark:border-border pb-2">
              <h4 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
                Connect Cluster URL: {connectHost}
              </h4>
              <p className="text-sm text-gray-500 dark:text-gray-400">
                {connectors.length} connector{connectors.length !== 1 ? 's' : ''}
              </p>
            </div>
            <div className="grid gap-4">{connectors.map(renderSelfManagedConnector)}</div>
          </div>
        ))}
      </div>
    )
  }

  // Check if we have any connectors at all
  const hasMSKConnectors = connectors && connectors.length > 0
  const hasSelfManagedConnectors = selfManagedConnectors && selfManagedConnectors.length > 0

  if (!hasMSKConnectors && !hasSelfManagedConnectors) {
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
          Kafka Connect Connectors
        </h3>
      </div>

      {/* Tab Navigation */}
      <div className="border-b border-gray-200 dark:border-border">
        <nav className="-mb-px flex space-x-8">
          <button
            onClick={() => setActiveTab(CONNECTOR_TABS.MSK)}
            className={`py-2 px-1 border-b-2 font-medium text-sm ${
              activeTab === CONNECTOR_TABS.MSK
                ? 'border-blue-500 text-blue-600 dark:text-accent'
                : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
            }`}
          >
            MSK Connectors ({connectors?.length || 0})
          </button>
          <button
            onClick={() => setActiveTab(CONNECTOR_TABS.SELF_MANAGED)}
            className={`py-2 px-1 border-b-2 font-medium text-sm ${
              activeTab === CONNECTOR_TABS.SELF_MANAGED
                ? 'border-blue-500 text-blue-600 dark:text-accent'
                : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
            }`}
          >
            Self Managed Connectors ({selfManagedConnectors?.length || 0})
          </button>
        </nav>
      </div>

      {/* Tab Content */}
      <div className="mt-6">
        {activeTab === CONNECTOR_TABS.MSK && renderMSKConnectors()}
        {activeTab === CONNECTOR_TABS.SELF_MANAGED && renderSelfManagedConnectors()}
      </div>
    </div>
  )
}
