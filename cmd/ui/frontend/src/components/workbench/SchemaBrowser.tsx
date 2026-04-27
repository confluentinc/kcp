import { useState } from 'react'
import { ChevronRight, ChevronDown } from 'lucide-react'
import { listTables } from '@/lib/duckdb/db'

export interface SchemaBrowserProps {
  onInsert?: (identifier: string) => void
}

export function SchemaBrowser({ onInsert }: SchemaBrowserProps) {
  const tables = listTables()
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const toggleTable = (name: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(name)) {
        next.delete(name)
      } else {
        next.add(name)
      }
      return next
    })
  }

  return (
    <div className="h-full overflow-y-auto text-sm">
      {tables.map((table) => {
        const isOpen = expanded.has(table.name)
        return (
          <div key={table.name}>
            {/* Table row */}
            <button
              className="w-full flex items-center gap-1 px-3 py-1.5 hover:bg-secondary/50 text-left group"
              onClick={() => {
                toggleTable(table.name)
                onInsert?.(table.name)
              }}
              title={table.description}
            >
              <span className="text-muted-foreground shrink-0">
                {isOpen ? (
                  <ChevronDown className="h-3 w-3" />
                ) : (
                  <ChevronRight className="h-3 w-3" />
                )}
              </span>
              <span className="flex-1 font-mono font-medium text-foreground truncate">
                {table.name}
              </span>
              <span className="text-xs text-muted-foreground shrink-0">
                {table.columns.length}
              </span>
            </button>

            {/* Columns */}
            {isOpen && (
              <div className="bg-secondary/20">
                {table.columns.map((col) => (
                  <button
                    key={col.name}
                    className="w-full flex items-center gap-2 pl-8 pr-3 py-1 hover:bg-secondary/50 text-left"
                    onClick={() => onInsert?.(`${table.name}.${col.name}`)}
                    title={col.description}
                  >
                    <span className="flex-1 font-mono text-xs text-foreground truncate">
                      {col.name}
                    </span>
                    <span className="text-xs px-1.5 py-0.5 rounded bg-secondary text-muted-foreground font-mono shrink-0">
                      {col.type}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
