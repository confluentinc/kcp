import { useState } from 'react'
import { useWorkbenchResult } from '@/stores/workbenchStore'
import { ResultsGrid } from './ResultsGrid'
import { ResultsChart } from './ResultsChart'

type ResultTab = 'grid' | 'chart'

export function WorkbenchResults() {
  const [activeTab, setActiveTab] = useState<ResultTab>('grid')
  const { result, error, isRunning } = useWorkbenchResult()

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Tab strip */}
      <div className="flex items-center border-b border-border bg-secondary/40 shrink-0">
        {(['grid', 'chart'] as ResultTab[]).map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={`px-4 py-2 text-sm font-medium capitalize transition-colors border-b-2 ${
              activeTab === tab
                ? 'border-accent text-accent'
                : 'border-transparent text-muted-foreground hover:text-foreground'
            }`}
          >
            {tab === 'grid' ? 'Grid' : 'Chart'}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="flex-1 min-h-0 overflow-hidden">
        {activeTab === 'grid' ? (
          <ResultsGrid result={result} error={error} loading={isRunning} />
        ) : (
          <ResultsChart result={result} />
        )}
      </div>
    </div>
  )
}
