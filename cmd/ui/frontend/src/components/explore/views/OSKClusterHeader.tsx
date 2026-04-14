import type { OSKCluster } from '@/types'

interface OSKClusterHeaderProps {
  cluster: OSKCluster
}

export const OSKClusterHeader = ({ cluster }: OSKClusterHeaderProps) => {
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
