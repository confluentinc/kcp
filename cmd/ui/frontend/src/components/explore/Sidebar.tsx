import { useAppStore } from '@/stores/store'
import { isMSKSource, isApacheKafkaSource } from '@/lib/sourceUtils'
import { MSKSourceSection } from './sidebar/MSKSourceSection'
import { ApacheKafkaSourceSection } from './sidebar/ApacheKafkaSourceSection'
import { Database } from 'lucide-react'

export const Sidebar = () => {
  const kcpState = useAppStore((state) => state.kcpState)
  const selectedView = useAppStore((state) => state.selectedView)
  const selectSchemaRegistries = useAppStore((state) => state.selectSchemaRegistries)

  // Extract sources
  const mskSource = kcpState?.sources.find(isMSKSource)
  const apacheKafkaSource = kcpState?.sources.find(isApacheKafkaSource)

  const hasMSK = mskSource !== undefined
  const hasApacheKafka = apacheKafkaSource !== undefined

  return (
    <div className="h-full flex flex-col">
      {/* Cluster Navigation - scrollable */}
      <div className="flex-1 overflow-y-auto p-3 space-y-5">
        {/* Show MSK section if MSK data exists */}
        {hasMSK && mskSource.msk_data && (
          <MSKSourceSection regions={mskSource.msk_data.regions} />
        )}

        {/* Show Apache Kafka section if Apache Kafka data exists */}
        {hasApacheKafka && apacheKafkaSource.apache_kafka_data && (
          <ApacheKafkaSourceSection clusters={apacheKafkaSource.apache_kafka_data.clusters} />
        )}

        {/* Empty state if no sources */}
        {!hasMSK && !hasApacheKafka && (
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
          className={`w-full text-left flex items-center px-2.5 py-2 rounded-md transition-all duration-150 group ${
            selectedView === 'schema-registries'
              ? 'bg-accent/10 text-accent'
              : 'text-foreground hover:text-accent hover:bg-secondary'
          }`}
        >
          <Database className={`w-4 h-4 mr-2.5 flex-shrink-0 ${selectedView === 'schema-registries' ? 'text-accent' : 'text-muted-foreground group-hover:text-accent'}`} />
          <span className="text-sm font-medium">Schema Registries</span>
        </button>
      </div>
    </div>
  )
}
