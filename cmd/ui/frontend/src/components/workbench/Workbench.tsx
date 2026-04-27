import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { Play, X, Loader2 } from 'lucide-react'
import { useAppStore } from '@/stores/store'
import { useWorkbenchStore, useWorkbenchDraft } from '@/stores/workbenchStore'
import { ensureLoaded, resetDB, listTables } from '@/lib/duckdb/db'
import { SqlEditor } from './SqlEditor'
import { WorkbenchResults } from './WorkbenchResults'
import { SchemaBrowser } from './SchemaBrowser'
import { SavedQueries } from './SavedQueries'
import { QueryHistory } from './QueryHistory'
import { Button } from '@/components/common/ui/button'
import { Modal } from '@/components/common/ui/modal'
import type { SavedQuery } from '@/lib/duckdb/schema'

type LeftTab = 'tables' | 'saved' | 'history'

// ---------------------------------------------------------------------------
// Save-query modal
// ---------------------------------------------------------------------------

const CATEGORIES: SavedQuery['category'][] = [
  'overview',
  'topics',
  'acls',
  'metrics',
  'costs',
  'connectors',
  'clients',
  'schemas',
]

interface SaveQueryModalProps {
  isOpen: boolean
  onClose: () => void
  initialSql: string
}

function SaveQueryModal({ isOpen, onClose, initialSql }: SaveQueryModalProps) {
  const addSavedQuery = useWorkbenchStore((s) => s.addSavedQuery)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [category, setCategory] = useState<SavedQuery['category']>('overview')
  const [sql, setSql] = useState(initialSql)

  // Reset form when opened
  useEffect(() => {
    if (isOpen) {
      setName('')
      setDescription('')
      setCategory('overview')
      setSql(initialSql)
    }
  }, [isOpen, initialSql])

  const canSave = name.trim() !== '' && description.trim() !== ''

  const handleSave = () => {
    if (!canSave) return
    addSavedQuery({ name: name.trim(), description: description.trim(), category, sql })
    onClose()
  }

  return (
    <Modal isOpen={isOpen} onClose={onClose} title="Save Query" className="!max-w-lg !h-auto !max-h-[80vh]">
      <div className="flex flex-col gap-4 p-2">
        {/* Name */}
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium text-foreground">
            Name <span className="text-destructive">*</span>
          </label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Topics with high retention"
            className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-accent"
          />
        </div>

        {/* Description */}
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium text-foreground">
            Description <span className="text-destructive">*</span>
          </label>
          <input
            type="text"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="What does this query show?"
            className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-accent"
          />
        </div>

        {/* Category */}
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium text-foreground">Category</label>
          <select
            value={category}
            onChange={(e) => setCategory(e.target.value as SavedQuery['category'])}
            className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-accent"
          >
            {CATEGORIES.map((cat) => (
              <option key={cat} value={cat}>
                {cat.charAt(0).toUpperCase() + cat.slice(1)}
              </option>
            ))}
          </select>
        </div>

        {/* SQL */}
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium text-foreground">SQL</label>
          <textarea
            value={sql}
            onChange={(e) => setSql(e.target.value)}
            rows={6}
            className="rounded-md border border-border bg-background px-3 py-2 text-sm font-mono outline-none focus:ring-1 focus:ring-accent resize-y"
          />
        </div>

        {/* Actions */}
        <div className="flex justify-end gap-2 pt-2">
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button size="sm" disabled={!canSave} onClick={handleSave}>
            Save
          </Button>
        </div>
      </div>
    </Modal>
  )
}

// ---------------------------------------------------------------------------
// Main Workbench component
// ---------------------------------------------------------------------------

export function Workbench() {
  const kcpState = useAppStore((s) => s.kcpState)
  const [isInitializing, setIsInitializing] = useState(false)
  const [initError, setInitError] = useState<string | null>(null)
  const [leftTab, setLeftTab] = useState<LeftTab>('tables')
  const [saveModalOpen, setSaveModalOpen] = useState(false)

  const queryDraft = useWorkbenchDraft()
  const setDraft = useWorkbenchStore((s) => s.setDraft)
  const runQuery = useWorkbenchStore((s) => s.runQuery)
  const isRunning = useWorkbenchStore((s) => s.isRunning)

  // Track the last state ref we ingested to detect changes
  const loadedStateRef = useRef<typeof kcpState>(null)

  useEffect(() => {
    if (!kcpState) return

    const isNewState = kcpState !== loadedStateRef.current

    const load = async () => {
      setIsInitializing(true)
      setInitError(null)
      try {
        if (isNewState && loadedStateRef.current !== null) {
          // State changed — reset DB first
          await resetDB()
        }
        await ensureLoaded(kcpState)
        loadedStateRef.current = kcpState
      } catch (err) {
        setInitError(err instanceof Error ? err.message : String(err))
      } finally {
        setIsInitializing(false)
      }
    }

    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kcpState])

  // Stable callbacks — read queryDraft from store at call time rather than
  // capturing it in the closure, so props to <SqlEditor> don't change every
  // render (which would invalidate its useMemo and reinit CodeMirror).
  const handleInsert = useCallback((identifier: string) => {
    const store = useWorkbenchStore.getState()
    const current = store.queryDraft
    store.setDraft(current ? current + ' ' + identifier : identifier)
  }, [])

  const handleSubmit = useCallback(() => {
    const store = useWorkbenchStore.getState()
    store.runQuery(store.queryDraft)
  }, [])

  // Tables list is static (derived from TABLE_METADATA) — memoize so its
  // reference is stable across renders.
  const editorTables = useMemo(() => Array.from(listTables()), [])

  if (!kcpState) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
        No state loaded. Upload a KCP state file to use the SQL Workbench.
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0">
      {/* Left pane */}
      <div className="w-72 shrink-0 border-r border-border flex flex-col min-h-0 bg-card">
        {/* Left tab strip */}
        <div className="flex shrink-0 border-b border-border">
          {([
            { id: 'tables', label: 'Tables' },
            { id: 'saved', label: 'Saved' },
            { id: 'history', label: 'History' },
          ] as { id: LeftTab; label: string }[]).map((tab) => (
            <button
              key={tab.id}
              onClick={() => setLeftTab(tab.id)}
              className={`flex-1 py-2 text-xs font-medium transition-colors border-b-2 ${
                leftTab === tab.id
                  ? 'border-accent text-accent'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>

        {/* Left pane content */}
        <div className="flex-1 min-h-0 overflow-hidden">
          {leftTab === 'tables' && <SchemaBrowser onInsert={handleInsert} />}
          {leftTab === 'saved' && (
            <SavedQueries
              onRun={(sql) => setDraft(sql)}
              onNewClick={() => setSaveModalOpen(true)}
            />
          )}
          {leftTab === 'history' && (
            <QueryHistory onLoad={(sql) => setDraft(sql)} />
          )}
        </div>
      </div>

      {/* Right pane */}
      <div className="flex-1 flex flex-col min-h-0 min-w-0">
        {/* Toolbar */}
        <div className="flex items-center gap-2 px-3 py-2 border-b border-border bg-card shrink-0">
          <Button
            size="sm"
            className="bg-accent text-accent-foreground hover:bg-accent/90 gap-1.5"
            disabled={isRunning || queryDraft.trim() === '' || isInitializing}
            onClick={() => runQuery(queryDraft)}
          >
            {isRunning ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Play className="h-3.5 w-3.5" />
            )}
            {isRunning ? 'Running...' : 'Run'}
            <span className="ml-1 text-xs opacity-70">Cmd+Enter</span>
          </Button>

          <Button
            variant="ghost"
            size="sm"
            disabled={queryDraft === ''}
            onClick={() => setDraft('')}
            className="gap-1"
          >
            <X className="h-3.5 w-3.5" />
            Clear
          </Button>

          <Button
            variant="outline"
            size="sm"
            onClick={() => setSaveModalOpen(true)}
            className="ml-auto"
          >
            Save query
          </Button>

          {/* Init status */}
          {isInitializing && (
            <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" />
              Loading data...
            </span>
          )}
          {initError && (
            <span className="text-xs text-destructive" title={initError}>
              DB error
            </span>
          )}
        </div>

        {/* Editor */}
        <div className="px-3 pt-3 pb-2 shrink-0">
          <SqlEditor
            value={queryDraft}
            onChange={setDraft}
            onSubmit={handleSubmit}
            tables={editorTables}
            height="200px"
          />
        </div>

        {/* Results */}
        <div className="flex-1 min-h-0 border-t border-border">
          <WorkbenchResults />
        </div>
      </div>

      {/* Save query modal */}
      <SaveQueryModal
        isOpen={saveModalOpen}
        onClose={() => setSaveModalOpen(false)}
        initialSql={queryDraft}
      />
    </div>
  )
}
