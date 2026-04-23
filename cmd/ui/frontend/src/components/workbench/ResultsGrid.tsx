import { useRef, useState, useMemo, useCallback } from 'react'
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  flexRender,
  type SortingState,
  type ColumnDef,
} from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { QueryResult, QueryError, QueryResultColumn } from '@/lib/duckdb/schema'
import { Button } from '@/components/common/ui/button'

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export interface ResultsGridProps {
  result: QueryResult | null
  error?: QueryError | null
  loading?: boolean
}

// ---------------------------------------------------------------------------
// Cell renderer
// ---------------------------------------------------------------------------

function renderCell(value: unknown, jsType: QueryResultColumn['jsType']): React.ReactNode {
  if (value === null || value === undefined) {
    return <span className="text-muted-foreground">—</span>
  }

  switch (jsType) {
    case 'bigint': {
      if (typeof value === 'bigint') {
        if (value >= BigInt(Number.MIN_SAFE_INTEGER) && value <= BigInt(Number.MAX_SAFE_INTEGER)) {
          return (
            <span className="font-mono text-right block">
              {Number(value).toLocaleString()}
            </span>
          )
        }
        return <span className="font-mono text-right block">{String(value)}</span>
      }
      if (typeof value === 'number' && isFinite(value)) {
        return <span className="font-mono text-right block">{value.toLocaleString()}</span>
      }
      return <span className="font-mono text-right block">{String(value)}</span>
    }

    case 'number': {
      if (typeof value === 'number') {
        return <span className="font-mono text-right block">{value.toLocaleString()}</span>
      }
      return <span className="font-mono text-right block">{String(value)}</span>
    }

    case 'boolean': {
      const bVal = Boolean(value)
      return bVal ? (
        <span className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-success/10 text-success">
          true
        </span>
      ) : (
        <span className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-secondary text-muted-foreground">
          false
        </span>
      )
    }

    case 'date': {
      return <span className="font-mono text-xs">{String(value)}</span>
    }

    case 'string': {
      return <>{String(value)}</>
    }

    case 'null': {
      return <span className="text-muted-foreground">—</span>
    }

    case 'unknown':
    default: {
      const raw = JSON.stringify(value) ?? String(value)
      return <>{raw.length > 80 ? raw.slice(0, 77) + '...' : raw}</>
    }
  }
}

// ---------------------------------------------------------------------------
// Skeleton loading state
// ---------------------------------------------------------------------------

function GridSkeleton() {
  return (
    <div className="h-full flex flex-col">
      {/* Header skeleton */}
      <div className="flex gap-2 px-3 py-2 border-b border-border bg-secondary">
        {[120, 80, 100, 90, 110].map((w, i) => (
          <div
            key={i}
            className="h-4 rounded bg-muted animate-pulse"
            style={{ width: w }}
          />
        ))}
      </div>
      {/* Row skeletons */}
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="flex gap-2 px-3 py-2 border-b border-border">
          {[120, 80, 100, 90, 110].map((w, j) => (
            <div
              key={j}
              className="h-4 rounded bg-muted animate-pulse opacity-60"
              style={{ width: w, opacity: 0.6 - i * 0.07 }}
            />
          ))}
        </div>
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Error state
// ---------------------------------------------------------------------------

function GridError({ error }: { error: QueryError }) {
  return (
    <div className="m-4 rounded-lg border border-destructive/50 bg-destructive/10 p-4">
      <p className="font-semibold text-destructive text-sm mb-2">{error.message}</p>
      <details className="text-xs text-muted-foreground">
        <summary className="cursor-pointer hover:text-foreground mb-1">Details</summary>
        <div className="mt-2 space-y-1">
          <div>
            <span className="font-medium">SQL:</span>
            <pre className="mt-1 rounded bg-secondary p-2 overflow-x-auto whitespace-pre-wrap break-words">
              {error.sql}
            </pre>
          </div>
          <div>
            <span className="font-medium">Duration:</span> {error.durationMs.toFixed(1)}ms
          </div>
        </div>
      </details>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Copy utilities
// ---------------------------------------------------------------------------

function csvEscape(val: unknown): string {
  if (val === null || val === undefined) return ''
  const s = String(val)
  if (s.includes(',') || s.includes('"') || s.includes('\n')) {
    return '"' + s.replace(/"/g, '""') + '"'
  }
  return s
}

function buildCSV(columns: QueryResultColumn[], rows: Record<string, unknown>[]): string {
  const header = columns.map((c) => csvEscape(c.name)).join(',')
  const body = rows
    .map((row) => columns.map((c) => csvEscape(row[c.name])).join(','))
    .join('\r\n')
  return header + '\r\n' + body + '\r\n'
}

// ---------------------------------------------------------------------------
// Footer with copy buttons
// ---------------------------------------------------------------------------

function GridFooter({ result }: { result: QueryResult }) {
  const [toast, setToast] = useState<string | null>(null)

  const flash = (msg: string) => {
    setToast(msg)
    setTimeout(() => setToast(null), 1500)
  }

  const copyJSON = useCallback(async () => {
    await navigator.clipboard.writeText(JSON.stringify(result.rows, null, 2))
    flash('Copied')
  }, [result.rows])

  const copyCSV = useCallback(async () => {
    await navigator.clipboard.writeText(buildCSV(result.columns, result.rows))
    flash('Copied')
  }, [result.columns, result.rows])

  return (
    <div className="flex items-center justify-between px-3 py-1.5 border-t border-border bg-secondary/40 text-xs text-muted-foreground shrink-0">
      <span>
        {result.rowCount.toLocaleString()} rows · {result.durationMs.toFixed(1)}ms
      </span>
      <div className="flex items-center gap-2">
        {toast && (
          <span className="text-xs text-green-600 font-medium transition-opacity">{toast}</span>
        )}
        <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={copyJSON}>
          Copy JSON
        </Button>
        <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={copyCSV}>
          Copy CSV
        </Button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Custom sort function for date columns
// ---------------------------------------------------------------------------

const dateSortFn = (rowA: { getValue: (id: string) => unknown }, rowB: { getValue: (id: string) => unknown }, columnId: string): number => {
  const a = rowA.getValue(columnId)
  const b = rowB.getValue(columnId)
  const aMs = a ? Date.parse(String(a)) : -Infinity
  const bMs = b ? Date.parse(String(b)) : -Infinity
  return aMs < bMs ? -1 : aMs > bMs ? 1 : 0
}

// ---------------------------------------------------------------------------
// Virtualized grid
// ---------------------------------------------------------------------------

function VirtualGrid({ result }: { result: QueryResult }) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [sorting, setSorting] = useState<SortingState>([])

  const columns = useMemo<ColumnDef<Record<string, unknown>>[]>(() => {
    return result.columns.map((col) => {
      const sortFn =
        col.jsType === 'date'
          ? dateSortFn
          : col.jsType === 'number' || col.jsType === 'bigint'
          ? 'basic'
          : 'alphanumeric'

      return {
        id: col.name,
        accessorKey: col.name,
        sortingFn: sortFn as 'alphanumeric' | 'basic' | typeof dateSortFn,
        header: () => (
          <span className="flex items-center gap-1 whitespace-nowrap">
            {col.name}
            <span className="text-xs px-1.5 py-0.5 rounded bg-secondary text-muted-foreground ml-2 font-mono">
              {col.jsType}
            </span>
          </span>
        ),
        cell: ({ getValue }) => {
          const val = getValue()
          return renderCell(val, col.jsType)
        },
      }
    })
  }, [result.columns])

  const table = useReactTable({
    data: result.rows,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  })

  const { rows } = table.getRowModel()

  const virtualizer = useVirtualizer({
    count: rows.length,
    estimateSize: () => 32,
    overscan: 10,
    getScrollElement: () => scrollRef.current,
  })

  const virtualItems = virtualizer.getVirtualItems()

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Scrollable area */}
      <div ref={scrollRef} className="h-full overflow-auto relative">
        <table className="w-full border-collapse text-sm">
          {/* Sticky header */}
          <thead className="sticky top-0 z-10 bg-secondary border-b border-border">
            {table.getHeaderGroups().map((hg) => (
              <tr key={hg.id}>
                {hg.headers.map((header) => (
                  <th
                    key={header.id}
                    className="px-3 py-1.5 text-left text-xs font-semibold text-foreground border-b border-border whitespace-nowrap cursor-pointer select-none hover:bg-secondary/80"
                    onClick={header.column.getToggleSortingHandler()}
                    style={{ width: header.getSize() }}
                  >
                    <span className="inline-flex items-center gap-1">
                      {flexRender(header.column.columnDef.header, header.getContext())}
                      {header.column.getIsSorted() === 'asc' && ' ↑'}
                      {header.column.getIsSorted() === 'desc' && ' ↓'}
                    </span>
                  </th>
                ))}
              </tr>
            ))}
          </thead>
          <tbody>
            {/* Padding top for virtualization */}
            {virtualItems.length > 0 && virtualItems[0].start > 0 && (
              <tr>
                <td
                  colSpan={columns.length}
                  style={{ height: virtualItems[0].start }}
                />
              </tr>
            )}
            {virtualItems.map((vItem) => {
              const row = rows[vItem.index]
              return (
                <tr
                  key={row.id}
                  data-index={vItem.index}
                  ref={virtualizer.measureElement}
                  className="hover:bg-secondary/30"
                >
                  {row.getVisibleCells().map((cell) => (
                    <td
                      key={cell.id}
                      className="px-3 py-1 text-sm truncate border-b border-border max-w-xs"
                    >
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  ))}
                </tr>
              )
            })}
            {/* Padding bottom for virtualization */}
            {virtualItems.length > 0 && (
              <tr>
                <td
                  colSpan={columns.length}
                  style={{
                    height:
                      virtualizer.getTotalSize() -
                      (virtualItems[virtualItems.length - 1]?.end ?? 0),
                  }}
                />
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <GridFooter result={result} />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main export
// ---------------------------------------------------------------------------

export function ResultsGrid({ result, error, loading }: ResultsGridProps) {
  if (loading) {
    return <GridSkeleton />
  }

  if (error) {
    return <GridError error={error} />
  }

  if (!result) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
        No results yet. Run a query with Cmd/Ctrl+Enter.
      </div>
    )
  }

  if (result.rowCount === 0) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
        Query returned no rows · {result.durationMs.toFixed(1)}ms
      </div>
    )
  }

  return <VirtualGrid result={result} />
}
