import { RegionCosts } from '../regions/RegionCosts'
import { useSelectedRegion } from '@/stores/store'

export const RegionReport = () => {
  const region = useSelectedRegion()

  if (!region) {
    return (
      <div className="p-6">
        <div className="bg-destructive/10 border border-destructive/20 rounded-lg p-4">
          <p className="text-destructive">
            Region not found. Please select a region from the sidebar.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="w-full space-y-6">
      {/* Region Header */}
      <div className="bg-card rounded-lg shadow-sm border border-border p-6 transition-colors">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold text-foreground">
              Region:&nbsp;{region.name}
            </h1>
          </div>
        </div>
      </div>

      {/* Region Costs Component */}
      <RegionCosts
        region={region}
        isActive={true}
      />
    </div>
  )
}
