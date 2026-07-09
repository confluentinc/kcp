import { useEffect, useState } from 'react'
import { Button } from '@/components/common/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/common/ui/select'
import { formatDate } from '@/lib/formatters'
import { hasRedactedConfig } from '@/lib/redaction'
import { CONNECTOR_TABS, REDACTED_PLACEHOLDER } from '@/constants'
import { ConnectMetrics } from './ConnectMetrics'
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
  connectMetrics?: {
    metadata?: {
      start_date?: string
      end_date?: string
      period?: number
    }
  }
  // MSK-managed Connect (CloudWatch-sourced) metrics. Only ever present for MSK
  // clusters; OSK has no notion of a managed Connect service.
  managedConnectMetrics?: {
    metadata?: {
      start_date?: string
      end_date?: string
      period?: number
      metrics_source?: string
    }
  }
  clusterId?: string
  // Required: a Connect cluster is distribution-agnostic, so every caller must state
  // which source set the connect-metrics endpoint should search. Leaving it optional
  // risks an MSK caller silently falling back to the OSK source and 404-ing.
  sourceType: 'msk' | 'osk'
}

export const ClusterConnectors = ({
  connectors,
  selfManagedConnectors = [],
  connectMetrics,
  managedConnectMetrics,
  clusterId,
  sourceType,
}: ClusterConnectorsProps) => {
  const [activeTab, setActiveTab] = useState<ConnectorTab>(CONNECTOR_TABS.MSK)
  const [copiedConnector, setCopiedConnector] = useState<string | null>(null)
  // The MSK tab is scoped to a single connector chosen from a dropdown; this
  // drives both the metrics block and the summary card shown below it.
  const [selectedConnector, setSelectedConnector] = useState<string>('')

  // Keep the selection valid as the connector list changes (e.g. after a re-scan):
  // default to the first connector, and fall back to it if the current one vanishes.
  useEffect(() => {
    if (connectors.length > 0 && !connectors.some((c) => c.connector_name === selectedConnector)) {
      setSelectedConnector(connectors[0].connector_name)
    }
  }, [connectors, selectedConnector])

  const handleCopyConfig = (connectorName: string, config: Record<string, string>) => {
    const configText = Object.entries(config)
      .map(([key, value]) => `${key}=${value}`)
      .join('\n')
    navigator.clipboard.writeText(configText)
    setCopiedConnector(connectorName)
    setTimeout(() => setCopiedConnector(null), 2000)
  }

  const renderSelfManagedConnector = (connector: SelfManagedConnector) => (
    <div
      key={connector.name}
      className="bg-card border border-border rounded-lg shadow-sm transition-colors"
    >
      {/* Connector Header */}
      <div className="p-6 border-b border-border bg-secondary">
        <div className="flex items-start justify-between">
          <div className="flex-1">
            <div className="flex items-center gap-3 mb-2">
              <h4 className="text-xl font-semibold text-foreground">
                {connector.name}
              </h4>
            </div>
          </div>
        </div>
      </div>

      {/* Connector Configuration */}
      <div className="p-6">
        <div className="flex items-center justify-between mb-3">
          <h5 className="font-medium text-foreground">Connector Configuration</h5>
          <Button
            onClick={() => handleCopyConfig(connector.name, connector.config)}
            variant="outline"
            size="sm"
          >
            {copiedConnector === connector.name ? 'Copied!' : 'Copy Config'}
          </Button>
        </div>

        <textarea
          readOnly
          value={Object.entries(connector.config)
            .map(([key, value]) => `${key}=${value}`)
            .join('\n')}
          className="w-full h-48 p-3 text-sm font-mono bg-secondary border border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 dark:text-gray-100"
        />
      </div>
    </div>
  )

  const renderConnectorSelector = () => (
    <div className="flex items-center gap-4">
      <label className="text-sm font-medium text-foreground">Connector:</label>
      <Select
        value={selectedConnector}
        onValueChange={setSelectedConnector}
      >
        <SelectTrigger className="w-[360px]">
          <SelectValue placeholder="Choose a connector" />
        </SelectTrigger>
        <SelectContent>
          {connectors.map((connector) => (
            <SelectItem
              key={connector.connector_arn}
              value={connector.connector_name}
            >
              {connector.connector_name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )

  const renderManagedConnectMetrics = () =>
    managedConnectMetrics?.metadata && clusterId && (
      <ConnectMetrics
        clusterId={clusterId}
        sourceType={sourceType}
        kind="managed"
        connectorName={selectedConnector}
        connectMetricsMetadata={managedConnectMetrics.metadata}
      />
    )

  const renderMSKConnectors = () => {
    if (!connectors || connectors.length === 0) {
      return (
        <div className="space-y-8">
          {renderManagedConnectMetrics()}
          <div className="text-center py-12">
            <div className="text-muted-foreground text-lg">No MSK connectors found</div>
            <p className="text-sm text-muted-foreground mt-2">
              This cluster doesn't have any MSK Connect connectors configured.
            </p>
          </div>
        </div>
      )
    }

    return (
      <div className="space-y-8">
        {renderConnectorSelector()}
        {renderManagedConnectMetrics()}
        <div className="grid gap-6">
          {connectors
            .filter((connector) => connector.connector_name === selectedConnector)
            .map((connector) => (
            <div
              key={connector.connector_arn}
              className="bg-card border border-border rounded-lg shadow-sm transition-colors"
            >
              {/* Connector Header */}
              <div className="p-6 border-b border-border bg-secondary">
                <div className="flex items-start justify-between">
                  <div className="flex-1">
                    <div className="flex items-center gap-3 mb-2">
                      <h4 className="text-xl font-semibold text-foreground">
                        {connector.connector_name}
                      </h4>
                    </div>
                    <p className="text-sm text-muted-foreground">
                      Created: {formatDate(connector.creation_time)}
                    </p>
                    <p className="text-xs text-muted-foreground font-mono mt-1">
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
                    <h5 className="font-medium text-foreground mb-3">
                      Authentication
                    </h5>
                    <div className="space-y-2 text-sm">
                      <div className="flex justify-between">
                        <span className="text-muted-foreground">Type:</span>
                        <span className="font-medium text-foreground">
                          {connector.kafka_cluster_client_authentication.AuthenticationType}
                        </span>
                      </div>
                    </div>
                  </div>

                  <div className="lg:px-6">
                    <h5 className="font-medium text-foreground mb-3">Capacity</h5>
                    <div className="space-y-2 text-sm">
                      {connector.capacity.AutoScaling ? (
                        <>
                          <div className="flex justify-between">
                            <span className="text-muted-foreground">Min Workers:</span>
                            <span className="font-medium text-foreground">
                              {connector.capacity.AutoScaling.MinWorkerCount}
                            </span>
                          </div>
                          <div className="flex justify-between">
                            <span className="text-muted-foreground">Max Workers:</span>
                            <span className="font-medium text-foreground">
                              {connector.capacity.AutoScaling.MaxWorkerCount}
                            </span>
                          </div>
                          <div className="flex justify-between">
                            <span className="text-muted-foreground">MCU Count:</span>
                            <span className="font-medium text-foreground">
                              {connector.capacity.AutoScaling.McuCount}
                            </span>
                          </div>
                        </>
                      ) : (
                        <div className="text-muted-foreground">
                          Provisioned capacity configuration
                        </div>
                      )}
                    </div>
                  </div>

                  {/* Auto Scaling Policies */}
                  {connector.capacity.AutoScaling && (
                    <div className="lg:pl-6">
                      <h5 className="font-medium text-foreground mb-3">
                        Auto Scaling Policies
                      </h5>
                      <div className="space-y-2">
                        <div className="text-sm text-foreground">
                          <span className="font-medium">Scale Out Policy</span> CPU ≥{' '}
                          {connector.capacity.AutoScaling.ScaleOutPolicy.CpuUtilizationPercentage}%
                        </div>
                        <div className="text-sm text-foreground">
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
                    <h5 className="font-medium text-foreground">
                      Connector Configuration
                    </h5>
                    <Button
                      onClick={() => handleCopyConfig(connector.connector_arn, connector.connector_configuration)}
                      variant="outline"
                      size="sm"
                    >
                      {copiedConnector === connector.connector_arn ? 'Copied!' : 'Copy Config'}
                    </Button>
                  </div>

                  <textarea
                    readOnly
                    value={Object.entries(connector.connector_configuration)
                      .map(([key, value]) => `${key}=${value}`)
                      .join('\n')}
                    className="w-full h-48 p-3 text-sm font-mono bg-secondary border border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 dark:text-gray-100"
                  />
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    )
  }

  const renderSelfManagedConnectors = () => {
    if (!selfManagedConnectors || selfManagedConnectors.length === 0) {
      return (
        <div className="text-center py-12">
          <div className="text-muted-foreground text-lg">
            No self-managed connectors found
          </div>
          <p className="text-sm text-muted-foreground mt-2">
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
        {connectMetrics?.metadata && clusterId && (
          <ConnectMetrics
            clusterId={clusterId}
            sourceType={sourceType}
            connectMetricsMetadata={connectMetrics.metadata}
          />
        )}

        {Object.entries(groupedConnectors).map(([connectHost, connectors]) => (
          <div
            key={connectHost}
            className="space-y-4"
          >
            <div className="border-b border-border pb-2">
              <h4 className="text-lg font-semibold text-foreground">
                Connect Cluster URL: {connectHost}
              </h4>
              <p className="text-sm text-muted-foreground">
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
  // Metrics-only cases: a cluster can have Connect metrics collected without any
  // connectors having been discovered (e.g. permissions gaps enumerating connectors).
  // Treat that as "has content" too, so the tabs (and metrics block) still render.
  const hasConnectMetrics = Boolean(connectMetrics?.metadata) || Boolean(managedConnectMetrics?.metadata)

  // Whether any displayed connector (either tab) still carries the redaction
  // placeholder and therefore needs manual secret replacement before applying.
  // Guard against null props (a state file may carry `connectors: null`); the
  // `= []` default only covers undefined.
  const hasRedactedConnectorConfig =
    (connectors ?? []).some((c) => hasRedactedConfig(c.connector_configuration)) ||
    (selfManagedConnectors ?? []).some((c) => hasRedactedConfig(c.config))

  if (!hasMSKConnectors && !hasSelfManagedConnectors && !hasConnectMetrics) {
    return (
      <div className="text-center py-12">
        <div className="text-muted-foreground text-lg">No connectors found</div>
        <p className="text-sm text-muted-foreground mt-2">
          This cluster doesn't have any Kafka Connect connectors configured.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-foreground">
          Kafka Connect Connectors
        </h3>
      </div>

      {/* Redaction warning — shown when any connector config still carries the
          placeholder. Presentational only; never dumps the redacted values. */}
      {hasRedactedConnectorConfig && (
        <div
          data-testid="redacted-config-warning"
          className="p-4 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-border rounded-lg text-sm text-amber-800 dark:text-amber-200"
        >
          ⚠️ Some connector configs contain redacted sensitive fields (
          <span className="font-mono">{REDACTED_PLACEHOLDER}</span>). Replace these
          with real values before applying generated migration assets.
        </div>
      )}

      {/* Tab Navigation */}
      <div className="border-b border-border">
        <nav className="-mb-px flex space-x-8">
          <button
            onClick={() => setActiveTab(CONNECTOR_TABS.MSK)}
            className={`py-2 px-1 border-b-2 font-medium text-sm ${
              activeTab === CONNECTOR_TABS.MSK
                ? 'border-accent text-accent'
                : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
            }`}
          >
            MSK Connectors ({connectors?.length || 0})
          </button>
          <button
            onClick={() => setActiveTab(CONNECTOR_TABS.SELF_MANAGED)}
            className={`py-2 px-1 border-b-2 font-medium text-sm ${
              activeTab === CONNECTOR_TABS.SELF_MANAGED
                ? 'border-accent text-accent'
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

