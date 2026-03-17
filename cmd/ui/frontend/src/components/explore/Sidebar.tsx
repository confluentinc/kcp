import { useAppStore } from '@/stores/store'
import { isMSKSource, isOSKSource } from '@/lib/sourceUtils'
import { MSKSourceSection } from './sidebar/MSKSourceSection'
import { OSKSourceSection } from './sidebar/OSKSourceSection'

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
        <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
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
          <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-border rounded-lg p-4">
            <p className="text-sm text-yellow-800 dark:text-yellow-200">
              No clusters available. Please upload a KCP state file to explore your infrastructure.
            </p>
          </div>
        )}
      </div>

      {/* Schema Registries Section */}
      <div className="border-t border-gray-200 dark:border-border p-4">
        <div className="space-y-2">
          <p className="text-sm text-gray-600 dark:text-gray-400 px-2">
            Explore Schema Registries
          </p>

          <button
            onClick={selectSchemaRegistries}
            className={`w-full text-left flex items-center justify-between p-3 rounded-lg transition-colors ${
              selectedView === 'schema-registries'
                ? 'bg-blue-100 dark:bg-accent/20 border border-blue-200 dark:border-accent'
                : 'hover:bg-gray-100 dark:hover:bg-gray-600'
            }`}
          >
            <div className="flex items-center space-x-2 min-w-0 flex-1">
              <div
                className={`w-2 h-2 rounded-full flex-shrink-0 ${
                  selectedView === 'schema-registries' ? 'bg-blue-600' : 'bg-gray-500'
                }`}
              />
              <h4
                className={`text-sm whitespace-nowrap ${
                  selectedView === 'schema-registries'
                    ? 'text-blue-900 dark:text-accent'
                    : 'text-gray-800 dark:text-gray-200'
                }`}
              >
                Schema Registries
              </h4>
            </div>
          </button>
        </div>
      </div>
    </div>
  )
}
