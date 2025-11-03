import { useMemo } from 'react'
import ClusterReport from './views/ClusterReport'
import RegionReport from './views/RegionReport'
import Summary from './views/Summary'
import SchemaRegistries from './views/SchemaRegistries'
import {
  useAppStore,
  useSelectedCluster,
  useSelectedRegion,
  useRegions,
  useSchemaRegistries,
} from '@/stores/store'

export default function Explore() {
  const selectedView = useAppStore((state) => state.selectedView)
  const selectedClusterData = useSelectedCluster()
  const selectedRegionData = useSelectedRegion()
  const regions = useRegions()
  const schemaRegistries = useSchemaRegistries()

  const activeView = useMemo(() => {
    if (selectedView === 'summary') {
      return <Summary />
    }

    if (selectedView === 'cluster' && selectedClusterData) {
      const regionData = regions.find((r) => r.name === selectedClusterData.regionName)
      if (!regionData) {
        return (
          <div className="max-w-7xl mx-auto">
            <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-6">
              <h2 className="text-xl font-semibold text-red-900 dark:text-red-200 mb-2">
                Error: Region Not Found
              </h2>
              <p className="text-red-700 dark:text-red-300">
                Region "{selectedClusterData.regionName}" was not found in the available regions.
              </p>
            </div>
          </div>
        )
      }
      return (
        <ClusterReport
          cluster={selectedClusterData.cluster}
          regionName={selectedClusterData.regionName}
          regionData={regionData}
        />
      )
    }

    if (selectedView === 'region' && selectedRegionData) {
      return <RegionReport region={selectedRegionData} />
    }

    if (selectedView === 'schema-registries') {
      return <SchemaRegistries schemaRegistries={schemaRegistries} />
    }

    return null
  }, [selectedView, selectedClusterData, selectedRegionData, regions, schemaRegistries])

  return <div className="mx-auto space-y-6 w-full min-w-0 max-w-full">{activeView}</div>
}
