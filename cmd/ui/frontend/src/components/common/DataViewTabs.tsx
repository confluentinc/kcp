import type { ReactNode } from 'react'
import { Button } from '@/components/common/ui/button'
import { Download } from 'lucide-react'
import { Tabs } from './Tabs'
import { MetricsCodeViewer } from '@/components/explore/clusters/MetricsCodeViewer'
import { TAB_IDS } from '@/constants'
import type { TabId } from '@/types'

interface DataViewTabsProps {
  activeTab: string
  onTabChange: (id: TabId) => void
  onDownloadJSON: () => void
  onDownloadCSV: () => void
  jsonData: unknown
  csvData: string
  renderChart: () => ReactNode
  renderTable: () => ReactNode
  contentWrapperClassName?: string
}

/**
 * Reusable component for data view tabs (Chart/Table/JSON/CSV) with download buttons
 * Eliminates duplication between ClusterMetrics and RegionCosts components
 */
export const DataViewTabs = ({
  activeTab,
  onTabChange,
  onDownloadJSON,
  onDownloadCSV,
  jsonData,
  csvData,
  renderChart,
  renderTable,
  contentWrapperClassName = '',
}: DataViewTabsProps) => {
  const jsonString = JSON.stringify(jsonData, null, 2)

  return (
    <div className="w-full max-w-full">
      <div className="flex items-center justify-between mb-4">
        <Tabs
          tabs={[
            { id: TAB_IDS.CHART, label: 'Chart' },
            { id: TAB_IDS.TABLE, label: 'Table' },
            { id: TAB_IDS.JSON, label: 'JSON' },
            { id: TAB_IDS.CSV, label: 'CSV' },
          ]}
          activeId={activeTab}
          onChange={(id) => onTabChange(id as TabId)}
        />
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={onDownloadJSON}
            className="flex items-center gap-2"
          >
            <Download className="h-4 w-4" />
            JSON
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={onDownloadCSV}
            className="flex items-center gap-2"
          >
            <Download className="h-4 w-4" />
            CSV
          </Button>
        </div>
      </div>

      {/* Tab Content */}
      <div className={contentWrapperClassName || 'space-y-4 min-w-0'}>
        {activeTab === TAB_IDS.CHART && renderChart()}

        {activeTab === TAB_IDS.TABLE && renderTable()}

        {activeTab === TAB_IDS.JSON && (
          <MetricsCodeViewer
            data={jsonString}
            label="JSON"
            onCopy={() => navigator.clipboard.writeText(jsonString)}
            isJSON={true}
          />
        )}

        {activeTab === TAB_IDS.CSV && (
          <MetricsCodeViewer
            data={csvData}
            label="CSV"
            onCopy={() => navigator.clipboard.writeText(csvData)}
            isJSON={false}
          />
        )}
      </div>
    </div>
  )
}
