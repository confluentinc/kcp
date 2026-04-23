import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import { useShallow } from 'zustand/react/shallow'
import type { QueryResult, QueryError, SavedQuery } from '@/lib/duckdb/schema'
import { SEED_QUERIES } from '@/lib/workbench/seedQueries'
import { runQuery as dbRunQuery } from '@/lib/duckdb/db'

export interface HistoryEntry {
  id: string
  sql: string
  at: number
  durationMs: number
  rowCount: number | null
  error?: string
}

export interface WorkbenchState {
  queryDraft: string
  lastResult: QueryResult | null
  lastError: QueryError | null
  isRunning: boolean
  history: HistoryEntry[]
  saved: SavedQuery[]

  setDraft: (sql: string) => void
  runQuery: (sql: string) => Promise<void>
  addSavedQuery: (q: Omit<SavedQuery, 'id' | 'builtIn'>) => void
  deleteSavedQuery: (id: string) => void
  clearHistory: () => void
}

export const useWorkbenchStore = create<WorkbenchState>()(
  devtools(
    (set, _get) => ({
      queryDraft: '',
      lastResult: null,
      lastError: null,
      isRunning: false,
      history: [],
      saved: SEED_QUERIES,

      setDraft: (sql) => set({ queryDraft: sql }),

      runQuery: async (sql) => {
        set({ isRunning: true, lastError: null })
        try {
          const result = await dbRunQuery(sql)
          const entry: HistoryEntry = {
            id: crypto.randomUUID(),
            sql,
            at: Date.now(),
            durationMs: result.durationMs,
            rowCount: result.rowCount,
          }
          set((state) => ({
            lastResult: result,
            lastError: null,
            isRunning: false,
            history: [entry, ...state.history].slice(0, 50),
          }))
        } catch (err) {
          const queryError = err as QueryError
          const entry: HistoryEntry = {
            id: crypto.randomUUID(),
            sql,
            at: Date.now(),
            durationMs: queryError.durationMs ?? 0,
            rowCount: null,
            error: queryError.message ?? String(err),
          }
          set((state) => ({
            lastError: queryError,
            isRunning: false,
            history: [entry, ...state.history].slice(0, 50),
          }))
        }
      },

      addSavedQuery: (q) =>
        set((state) => ({
          saved: [
            ...state.saved,
            { ...q, id: `user-${crypto.randomUUID()}`, builtIn: false },
          ],
        })),

      deleteSavedQuery: (id) =>
        set((state) => {
          const target = state.saved.find((q) => q.id === id)
          if (!target || target.builtIn) return state // guard: no-op for missing or built-in
          return { saved: state.saved.filter((q) => q.id !== id) }
        }),

      clearHistory: () => set({ history: [] }),
    }),
    { name: 'kcp-workbench-store' }
  )
)

// ---------------------------------------------------------------------------
// Selector hooks for ergonomic consumption
// ---------------------------------------------------------------------------

export const useWorkbenchDraft = () => useWorkbenchStore((s) => s.queryDraft)
export const useWorkbenchResult = () =>
  useWorkbenchStore(
    useShallow((s) => ({ result: s.lastResult, error: s.lastError, isRunning: s.isRunning })),
  )
export const useWorkbenchHistory = () => useWorkbenchStore((s) => s.history)
export const useWorkbenchSaved = () => useWorkbenchStore((s) => s.saved)
