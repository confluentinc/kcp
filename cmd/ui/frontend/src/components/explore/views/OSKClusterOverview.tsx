import type { OSKCluster } from '@/types'
import { KeyValueGrid } from '@/components/common/KeyValueGrid'
import { KeyValuePair } from '@/components/common/KeyValuePair'

interface OSKClusterOverviewProps {
  cluster: OSKCluster
}

export const OSKClusterOverview = ({ cluster }: OSKClusterOverviewProps) => {
  return (
    <div className="space-y-6">
      {/* Bootstrap Servers */}
      <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
        <h3 className="text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100">
          Bootstrap Servers
        </h3>
        <div className="space-y-2">
          {cluster.bootstrap_servers.map((server, idx) => (
            <div
              key={idx}
              className="font-mono text-sm bg-gray-50 dark:bg-gray-800 p-3 rounded border border-gray-200 dark:border-border"
            >
              {server}
            </div>
          ))}
        </div>
      </div>

      {/* Metadata */}
      <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
        <h3 className="text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100">
          Cluster Metadata
        </h3>
        <KeyValueGrid>
          {cluster.metadata.environment && (
            <KeyValuePair label="Environment" value={cluster.metadata.environment} />
          )}
          {cluster.metadata.location && (
            <KeyValuePair label="Location" value={cluster.metadata.location} />
          )}
          {cluster.metadata.kafka_version && (
            <KeyValuePair label="Kafka Version" value={cluster.metadata.kafka_version} />
          )}
          {cluster.metadata.last_scanned && (
            <KeyValuePair
              label="Last Scanned"
              value={new Date(cluster.metadata.last_scanned).toLocaleString()}
            />
          )}
        </KeyValueGrid>
      </div>

      {/* Labels */}
      {cluster.metadata.labels && Object.keys(cluster.metadata.labels).length > 0 && (
        <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
          <h3 className="text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100">Labels</h3>
          <div className="flex flex-wrap gap-2">
            {Object.entries(cluster.metadata.labels).map(([key, value]) => (
              <span
                key={key}
                className="px-3 py-1 bg-blue-100 dark:bg-blue-900/20 text-blue-800 dark:text-blue-200 rounded-full text-sm font-medium"
              >
                {key}: {value}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
