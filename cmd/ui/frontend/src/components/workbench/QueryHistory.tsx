import { formatDistanceToNow } from 'date-fns'
import { useWorkbenchStore, useWorkbenchHistory } from '@/stores/workbenchStore'

export interface QueryHistoryProps {
  onLoad?: (sql: string) => void
}

export function QueryHistory({ onLoad }: QueryHistoryProps) {
  const history = useWorkbenchHistory()
  const clearHistory = useWorkbenchStore((s) => s.clearHistory)

  if (history.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-muted-foreground px-4 text-center">
        No queries run yet. Execute a query to see history here.
      </div>
    )
  }

  return (
    <div className="h-full flex flex-col overflow-hidden">
      {/* Clear button */}
      <div className="px-3 py-2 shrink-0 border-b border-border flex justify-end">
        <button
          onClick={clearHistory}
          className="text-xs text-muted-foreground hover:text-destructive transition-colors"
        >
          Clear history
        </button>
      </div>

      {/* History list */}
      <div className="flex-1 overflow-y-auto">
        {history.map((entry) => (
          <div
            key={entry.id}
            className="px-3 py-2 hover:bg-secondary/50 cursor-pointer border-b border-border last:border-0"
            onClick={() => onLoad?.(entry.sql)}
          >
            {/* Top row: time + status badge */}
            <div className="flex items-center justify-between gap-2 mb-1">
              <span className="text-xs text-muted-foreground shrink-0">
                {formatDistanceToNow(new Date(entry.at), { addSuffix: true })}
              </span>
              <div className="flex items-center gap-1.5">
                {entry.error ? (
                  <span className="text-xs px-1.5 py-0.5 rounded bg-destructive/10 text-destructive font-medium">
                    ERR
                  </span>
                ) : (
                  <span className="text-xs text-muted-foreground">
                    {entry.rowCount !== null ? `${entry.rowCount.toLocaleString()} rows` : '—'}
                  </span>
                )}
                <span className="text-xs text-muted-foreground">
                  {entry.durationMs.toFixed(0)}ms
                </span>
              </div>
            </div>

            {/* SQL preview */}
            <pre className="text-xs font-mono text-foreground truncate whitespace-pre">
              {entry.sql.length > 60 ? entry.sql.slice(0, 57) + '...' : entry.sql}
            </pre>
          </div>
        ))}
      </div>
    </div>
  )
}
