import { useAppStore } from '@/stores/store'
import { Summary } from './views/Summary'
import { RegionReport } from './views/RegionReport'
import { MSKClusterReport } from './views/MSKClusterReport'
import { OSKClusterReport } from './views/OSKClusterReport'
import { SchemaRegistries } from './views/SchemaRegistries'

export const Explore = () => {
  const selectedView = useAppStore((state) => state.selectedView)
  const selectedSourceType = useAppStore((state) => state.selectedSourceType)

  if (selectedView === 'summary') {
    return (
      <div className="h-full overflow-y-auto p-4">
        <Summary />
      </div>
    )
  }

  if (selectedView === 'region') {
    return (
      <div className="h-full overflow-y-auto p-4">
        <RegionReport />
      </div>
    )
  }

  if (selectedView === 'cluster') {
    // Route to appropriate cluster view based on source type
    if (selectedSourceType === 'msk') {
      return <MSKClusterReport />
    } else if (selectedSourceType === 'osk') {
      return <OSKClusterReport />
    }
    // Fallback for invalid state
    return (
      <div className="h-full overflow-y-auto p-4">
        <div className="bg-warning/10 border border-warning/20 rounded-lg p-4">
          <p className="text-warning">
            Unknown cluster source type. Please select a cluster from the sidebar.
          </p>
        </div>
      </div>
    )
  }

  if (selectedView === 'schema-registries') {
    return (
      <div className="h-full overflow-y-auto p-4">
        <SchemaRegistries />
      </div>
    )
  }

  // Default empty state
  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="text-center">
        <h1 className="text-4xl font-bold text-foreground mb-2">
          Explore Your Kafka Infrastructure
        </h1>
        <p className="text-lg text-muted-foreground">
          Upload a KCP state file or select a cluster from the sidebar
        </p>
      </div>
    </div>
  )
}
