import CodeMirror from '@uiw/react-codemirror'
import { sql, PostgreSQL } from '@codemirror/lang-sql'
import { oneDark } from '@codemirror/theme-one-dark'
import { keymap, EditorView } from '@codemirror/view'
import { Prec } from '@codemirror/state'
import { useEffect, useMemo, useState } from 'react'
import type { TableMeta } from '@/lib/duckdb/schema'

export interface SqlEditorProps {
  value: string
  onChange: (sql: string) => void
  onSubmit: () => void
  tables: TableMeta[]
  height?: string
  readOnly?: boolean
}

export function SqlEditor({
  value,
  onChange,
  onSubmit,
  tables,
  height = '200px',
  readOnly,
}: SqlEditorProps) {
  const [isDark, setIsDark] = useState(
    () => typeof document !== 'undefined' && document.documentElement.classList.contains('dark'),
  )

  useEffect(() => {
    if (typeof document === 'undefined') return
    const obs = new MutationObserver(() => {
      setIsDark(document.documentElement.classList.contains('dark'))
    })
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => obs.disconnect()
  }, [])

  const extensions = useMemo(() => {
    const schema = Object.fromEntries(tables.map((t) => [t.name, t.columns.map((c) => c.name)]))
    return [
      sql({ dialect: PostgreSQL, schema, upperCaseKeywords: true }),
      Prec.highest(
        keymap.of([
          {
            key: 'Mod-Enter',
            run: () => {
              onSubmit()
              return true
            },
          },
        ]),
      ),
      EditorView.theme({
        '&': {
          fontSize: '13px',
          fontFamily: 'var(--font-mono, ui-monospace, monospace)',
        },
      }),
    ]
  }, [tables, onSubmit])

  return (
    <div
      style={{ height }}
      className="border border-border rounded-md overflow-hidden focus-within:ring-1 focus-within:ring-accent"
    >
      <CodeMirror
        value={value}
        onChange={onChange}
        extensions={extensions}
        theme={isDark ? oneDark : undefined}
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          highlightActiveLineGutter: false,
          tabSize: 2,
        }}
        readOnly={readOnly}
        height="100%"
      />
    </div>
  )
}
