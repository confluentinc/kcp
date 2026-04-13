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
      <div className="p-4 pb-0">
        <p className="text-sm text-muted-foreground mt-1">
          Explore your Kafka infrastructure
        </p>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        {/* Show MSK section if MSK data exists */}
        {hasMSK && mskSource.msk_data && (
          <div className="mb-6">
            <MSKSourceSection regions={mskSource.msk_data.regions} />
          </div>
        )}

        {/* Show OSK section if OSK data exists */}
        {hasOSK && oskSource.osk_data && (
          <div className="mb-6">
            <OSKSourceSection clusters={oskSource.osk_data.clusters} />
          </div>
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

      {/* Schema Registries Section */}
      <div className="border-t border-border p-4">
        <div className="space-y-2">
          <p className="text-sm text-muted-foreground px-2">
            Explore Schema Registries
          </p>

          <button
            onClick={selectSchemaRegistries}
            className={`w-full text-left flex items-center p-2.5 rounded-lg transition-all duration-150 ${
              selectedView === 'schema-registries'
                ? 'bg-accent/10 text-accent border-l-[3px] border-accent'
                : 'hover:bg-secondary text-foreground'
            }`}
          >
            <Database className={`w-4 h-4 mr-2.5 flex-shrink-0 ${selectedView === 'schema-registries' ? 'text-accent' : 'text-muted-foreground'}`} />
            <span className="text-sm font-medium">Schema Registries</span>
          </button>
        </div>
      </div>
    </div>
  )
}
