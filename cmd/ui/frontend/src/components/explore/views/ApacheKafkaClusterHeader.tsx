import type { ApacheKafkaCluster } from '@/types'

interface ApacheKafkaClusterHeaderProps {
  cluster: ApacheKafkaCluster
}

export const ApacheKafkaClusterHeader = ({ cluster }: ApacheKafkaClusterHeaderProps) => {
  return (
    <div>
      <h1 className="text-2xl font-bold text-foreground">
        Cluster: {cluster.id}
      </h1>
      {cluster.metadata.last_scanned && (
        <p className="text-sm text-muted-foreground mt-1">
          Last scanned: {new Date(cluster.metadata.last_scanned).toLocaleString()}
        </p>
      )}
    </div>
  )
}
