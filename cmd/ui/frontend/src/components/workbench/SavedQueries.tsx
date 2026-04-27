import { useState } from 'react'
import { Trash2, Plus } from 'lucide-react'
import { ChevronRight, ChevronDown } from 'lucide-react'
import { useWorkbenchStore } from '@/stores/workbenchStore'
import type { SavedQuery } from '@/lib/duckdb/schema'

export interface SavedQueriesProps {
  onRun?: (sql: string) => void
  onNewClick?: () => void
}

type Category = SavedQuery['category']

const CATEGORY_ORDER: Category[] = [
  'overview',
  'topics',
  'acls',
  'metrics',
  'costs',
  'connectors',
  'clients',
  'schemas',
]

const CATEGORY_LABELS: Record<Category, string> = {
  overview: 'Overview',
  topics: 'Topics',
  acls: 'ACLs',
  metrics: 'Metrics',
  costs: 'Costs',
  connectors: 'Connectors',
  clients: 'Clients',
  schemas: 'Schemas',
}

export function SavedQueries({ onRun, onNewClick }: SavedQueriesProps) {
  const saved = useWorkbenchStore((s) => s.saved)
  const deleteSavedQuery = useWorkbenchStore((s) => s.deleteSavedQuery)
  const [collapsedCategories, setCollapsedCategories] = useState<Set<Category>>(new Set())

  const toggleCategory = (cat: Category) => {
    setCollapsedCategories((prev) => {
      const next = new Set(prev)
      if (next.has(cat)) {
        next.delete(cat)
      } else {
        next.add(cat)
      }
      return next
    })
  }

  // Group queries by category, preserving CATEGORY_ORDER
  const grouped = CATEGORY_ORDER.reduce<Record<Category, SavedQuery[]>>(
    (acc, cat) => {
      acc[cat] = saved.filter((q) => q.category === cat)
      return acc
    },
    {} as Record<Category, SavedQuery[]>
  )

  return (
    <div className="h-full flex flex-col overflow-hidden">
      {/* New button */}
      <div className="px-3 py-2 shrink-0 border-b border-border">
        <button
          onClick={() => onNewClick?.()}
          className="flex items-center gap-1.5 text-xs text-accent hover:text-accent/80 font-medium transition-colors"
        >
          <Plus className="h-3.5 w-3.5" />
          New saved query
        </button>
      </div>

      {/* Query list */}
      <div className="flex-1 overflow-y-auto">
        {CATEGORY_ORDER.map((cat) => {
          const queries = grouped[cat]
          if (queries.length === 0) return null
          const isCollapsed = collapsedCategories.has(cat)

          return (
            <div key={cat}>
              {/* Section header */}
              <button
                className="w-full flex items-center gap-1 px-3 py-1.5 bg-secondary/30 hover:bg-secondary/50 text-left"
                onClick={() => toggleCategory(cat)}
              >
                <span className="text-muted-foreground shrink-0">
                  {isCollapsed ? (
                    <ChevronRight className="h-3 w-3" />
                  ) : (
                    <ChevronDown className="h-3 w-3" />
                  )}
                </span>
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  {CATEGORY_LABELS[cat]}
                </span>
                <span className="ml-auto text-xs text-muted-foreground">{queries.length}</span>
              </button>

              {/* Queries */}
              {!isCollapsed &&
                queries.map((q) => (
                  <div
                    key={q.id}
                    className="group flex items-start gap-1 px-3 py-1.5 hover:bg-secondary/50 cursor-pointer"
                    onClick={() => onRun?.(q.sql)}
                  >
                    <div className="flex-1 min-w-0">
                      <div className="text-sm font-medium text-foreground truncate">{q.name}</div>
                      <div className="text-xs text-muted-foreground truncate">{q.description}</div>
                    </div>
                    {!q.builtIn && (
                      <button
                        className="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-destructive/10 text-destructive"
                        onClick={(e) => {
                          e.stopPropagation()
                          deleteSavedQuery(q.id)
                        }}
                        title="Delete query"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    )}
                  </div>
                ))}
            </div>
          )
        })}
      </div>
    </div>
  )
}
