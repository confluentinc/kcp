import { useAppStore } from '@/stores/store'
import { isMSKSource, isOSKSource } from '@/lib/sourceUtils'
import { MSKSourceSection } from './sidebar/MSKSourceSection'
import { OSKSourceSection } from './sidebar/OSKSourceSection'
import { Database } from 'lucide-react'

export const Sidebar = () => {
  const kcpState = useAppStore((state) => state.kcpState)
  const selectedView = useAppStore((state) => state.selectedView)
  const selectSchemaRegistries = useAppStore((state) => state.selectSchemaRegistries)

  // Extract sources
  const mskSource = kcpState?.sources.find(isMSKSource)
  const oskSource = kcpState?.sources.find(isOSKSource)

  const hasMSK = mskSource !== undefined
  const hasOSK = oskSource !== undefined

  return (
    <div className="h-full flex flex-col">
      {/* Cluster Navigation - scrollable */}
      <div className="flex-1 overflow-y-auto p-3 space-y-5">
        {/* Show MSK section if MSK data exists */}
        {hasMSK && mskSource.msk_data && (
          <MSKSourceSection regions={mskSource.msk_data.regions} />
        )}

        {/* Show OSK section if OSK data exists */}
        {hasOSK && oskSource.osk_data && (
          <OSKSourceSection clusters={oskSource.osk_data.clusters} />
        )}

        {/* Empty state if no sources */}
        {!hasMSK && !hasOSK && (
          <div className="bg-warning/10 border border-warning/20 rounded-lg p-4">
            <p className="text-sm text-warning">
              No clusters available. Please upload a KCP state file to explore your infrastructure.
            </p>
          </div>
        )}
      </div>

      {/* Schema Registries - fixed bottom */}
      <div className="border-t border-border p-3">
        <button
          onClick={selectSchemaRegistries}
          className={`w-full text-left flex items-center px-2.5 py-2 rounded-md transition-all duration-150 ${
            selectedView === 'schema-registries'
              ? 'bg-accent/10 text-accent'
              : 'hover:bg-secondary text-foreground'
          }`}
        >
          <Database className={`w-4 h-4 mr-2.5 flex-shrink-0 ${selectedView === 'schema-registries' ? 'text-accent' : 'text-muted-foreground'}`} />
          <span className="text-sm font-medium">Schema Registries</span>
        </button>
      </div>
    </div>
  )
}
