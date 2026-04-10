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
    return <Summary />
  }

  if (selectedView === 'region') {
    return <RegionReport />
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
      <div className="p-6">
        <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-border rounded-lg p-4">
          <p className="text-yellow-800 dark:text-yellow-200">
            Unknown cluster source type. Please select a cluster from the sidebar.
          </p>
        </div>
      </div>
    )
  }

  if (selectedView === 'schema-registries') {
    return <SchemaRegistries />
  }

  // Default empty state
  return (
    <div className="p-6">
      <div className="text-center">
        <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100 mb-2">
          Explore Your Kafka Infrastructure
        </h1>
        <p className="text-lg text-gray-600 dark:text-gray-400">
          Upload a KCP state file or select a cluster from the sidebar
        </p>
      </div>
    </div>
  )
}
