import type { OSKCluster } from '@/types'

interface OSKClusterHeaderProps {
  cluster: OSKCluster
}

export const OSKClusterHeader = ({ cluster }: OSKClusterHeaderProps) => {
  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100">
          Cluster: {cluster.id}
        </h1>
        {cluster.metadata.last_scanned && (
          <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
            Last scanned: {new Date(cluster.metadata.last_scanned).toLocaleString()}
          </p>
        )}
      </div>

      {/* Key Metrics */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {cluster.metadata.kafka_version && (
          <div className="bg-card border border-border border-t-4 border-t-accent shadow-sm rounded-lg p-5">
            <div className="text-3xl font-semibold text-foreground">
              {cluster.metadata.kafka_version}
            </div>
            <div className="text-sm text-muted-foreground mt-1">Kafka Version</div>
          </div>
        )}

        {cluster.metadata.environment && (
          <div className="bg-card border border-border border-t-4 border-t-accent shadow-sm rounded-lg p-5">
            <div className="text-3xl font-semibold text-foreground capitalize">
              {cluster.metadata.environment}
            </div>
            <div className="text-sm text-muted-foreground mt-1">Environment</div>
          </div>
        )}

        {cluster.metadata.location && (
          <div className="bg-card border border-border border-t-4 border-t-accent shadow-sm rounded-lg p-5">
            <div className="text-3xl font-semibold text-foreground">
              {cluster.metadata.location}
            </div>
            <div className="text-sm text-muted-foreground mt-1">Location</div>
          </div>
        )}
      </div>
    </div>
  )
}
