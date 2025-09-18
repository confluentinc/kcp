import { useState, useRef } from 'react'
import Sidebar, { type Region, type Cluster } from '@/components/Sidebar'
import ClusterReport from '@/components/ClusterReport'
import RegionReport from '@/components/RegionReport'
import AppHeader from '@/components/AppHeader'

export default function Home() {
  const [regions, setRegions] = useState<Region[]>([])
  const [selectedCluster, setSelectedCluster] = useState<{
    cluster: Cluster
    regionName: string
  } | null>(null)
  const [selectedRegion, setSelectedRegion] = useState<Region | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const handleFileUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) return

    setRegions([])
    setSelectedCluster(null)

    const reader = new FileReader()
    reader.onload = async (e) => {
      try {
        const content = e.target?.result as string
        const parsed = JSON.parse(content)

        if (parsed && typeof parsed === 'object' && 'regions' in parsed) {
          const regionsData = (parsed as any).regions
          if (Array.isArray(regionsData)) {
            setRegions(regionsData)

            if (
              regionsData.length > 0 &&
              regionsData[0].clusters &&
              regionsData[0].clusters.length > 0
            ) {
              const firstRegion = regionsData[0]
              const firstCluster = firstRegion.clusters[0]
              setSelectedCluster({
                cluster: firstCluster,
                regionName: firstRegion.name,
              })
            }
          }
        }
      } catch {
        setRegions([])
        setSelectedCluster(null)
      }
    }
    reader.readAsText(file)
  }

  const triggerFileUpload = () => {
    if (fileInputRef.current) {
      fileInputRef.current.value = ''
    }
    fileInputRef.current?.click()
  }

  const handleClusterSelect = (cluster: Cluster, regionName: string) => {
    setSelectedCluster({ cluster, regionName })
    setSelectedRegion(null)
  }

  const handleRegionSelect = (region: Region) => {
    setSelectedRegion(region)
    setSelectedCluster(null)
  }

  return (
    <div className="min-h-svh flex flex-col w-full h-full bg-gray-50 dark:bg-gray-900 transition-colors">
      <AppHeader />

      <div className="flex flex-1">
        <Sidebar
          onFileUpload={triggerFileUpload}
          regions={regions}
          onClusterSelect={handleClusterSelect}
          onRegionSelect={handleRegionSelect}
          selectedCluster={selectedCluster}
          selectedRegion={selectedRegion}
        />

        <main className="flex flex-1 p-4 w-full">
          <input
            ref={fileInputRef}
            type="file"
            accept=".json"
            onChange={handleFileUpload}
            className="hidden"
          />

          <div className="mx-auto space-y-6 w-full">
            {selectedCluster ? (
              <ClusterReport
                cluster={selectedCluster.cluster}
                regionName={selectedCluster.regionName}
                regionData={regions.find((r) => r.name === selectedCluster.regionName) as any}
              />
            ) : selectedRegion ? (
              <RegionReport region={selectedRegion} />
            ) : null}
          </div>
        </main>
      </div>
    </div>
  )
}
