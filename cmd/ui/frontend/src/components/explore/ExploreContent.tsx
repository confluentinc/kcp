import { useMemo } from 'react'
import ClusterReport from './views/ClusterReport'
import RegionReport from './views/RegionReport'
import Summary from './views/Summary'
import SchemaRegistries from './views/SchemaRegistries'
import { useAppStore } from '@/stores/appStore'

export default function ExploreContent() {
  const selectedSummary = useAppStore((state) => state.selectedSummary)
  const selectedCluster = useAppStore((state) => state.selectedCluster)
  const selectedRegion = useAppStore((state) => state.selectedRegion)
  const selectedSchemaRegistries = useAppStore((state) => state.selectedSchemaRegistries)
  const regions = useAppStore((state) => state.regions)
  const schemaRegistries = useAppStore((state) => state.schemaRegistries)

  const activeView = useMemo(() => {
    if (selectedSummary) {
      return <Summary />
    }

    if (selectedCluster) {
      const regionData = regions.find((r) => r.name === selectedCluster.regionName)
      if (!regionData) {
        return (
          <div className="max-w-7xl mx-auto">
            <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-6">
              <h2 className="text-xl font-semibold text-red-900 dark:text-red-200 mb-2">
                Error: Region Not Found
              </h2>
              <p className="text-red-700 dark:text-red-300">
                Region "{selectedCluster.regionName}" was not found in the available regions.
              </p>
            </div>
          </div>
        )
      }
      return (
        <ClusterReport
          cluster={selectedCluster.cluster}
          regionName={selectedCluster.regionName}
          regionData={regionData}
        />
      )
    }

    if (selectedRegion) {
      return <RegionReport region={selectedRegion} />
    }

    if (selectedSchemaRegistries) {
      return <SchemaRegistries schemaRegistries={schemaRegistries} />
    }

    return null
  }, [selectedSummary, selectedCluster, selectedRegion, selectedSchemaRegistries, regions, schemaRegistries])

  return <div className="mx-auto space-y-6 w-full min-w-0 max-w-full">{activeView}</div>
}

