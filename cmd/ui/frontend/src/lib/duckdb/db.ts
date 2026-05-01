/**
 * db.ts — Workstream C: DuckDB-WASM integration layer
 *
 * Provides a lazy singleton AsyncDuckDB instance, state ingestion, query
 * execution, and schema introspection. All SQL runs in the browser via
 * DuckDB-WASM — nothing executes server-side.
 */

import * as duckdb from '@duckdb/duckdb-wasm'
import type { AsyncDuckDB } from '@duckdb/duckdb-wasm'
import { Type } from 'apache-arrow'

import type { ProcessedState } from '@/types/api/state'
import type { QueryResult, QueryResultColumn, QueryError, TableMeta } from './schema'
import { TABLE_METADATA, CREATE_STATEMENTS } from './schema'
import { normalize } from './normalize'

// ---------------------------------------------------------------------------
// WASM bundle URLs — resolved by Vite's `?url` import
// ---------------------------------------------------------------------------

import mvp_wasm from '@duckdb/duckdb-wasm/dist/duckdb-mvp.wasm?url'
import mvp_worker from '@duckdb/duckdb-wasm/dist/duckdb-browser-mvp.worker.js?url'
import eh_wasm from '@duckdb/duckdb-wasm/dist/duckdb-eh.wasm?url'
import eh_worker from '@duckdb/duckdb-wasm/dist/duckdb-browser-eh.worker.js?url'

const BUNDLES: duckdb.DuckDBBundles = {
  mvp: { mainModule: mvp_wasm, mainWorker: mvp_worker },
  eh: { mainModule: eh_wasm, mainWorker: eh_worker },
}

// ---------------------------------------------------------------------------
// Module-scope singletons
// ---------------------------------------------------------------------------

let dbPromise: Promise<AsyncDuckDB> | null = null
let loadedStateRef: ProcessedState | null = null

// ---------------------------------------------------------------------------
// Custom error class
// ---------------------------------------------------------------------------

export class WorkbenchQueryError extends Error implements QueryError {
  readonly sql: string
  readonly durationMs: number

  constructor(message: string, sql: string, durationMs: number) {
    super(message)
    this.name = 'WorkbenchQueryError'
    this.sql = sql
    this.durationMs = durationMs
  }
}

// ---------------------------------------------------------------------------
// Arrow type → jsType mapping
// ---------------------------------------------------------------------------

function arrowTypeToJsType(typeId: number): QueryResultColumn['jsType'] {
  switch (typeId) {
    case Type.Utf8:
    case Type.LargeUtf8:
      return 'string'
    case Type.Int8:
    case Type.Int16:
    case Type.Int32:
    case Type.Uint8:
    case Type.Uint16:
    case Type.Uint32:
    case Type.Float16:
    case Type.Float32:
    case Type.Float64:
      return 'number'
    case Type.Int:
    case Type.Float:
      return 'number'
    case Type.Int64:
    case Type.Uint64:
      return 'bigint'
    case Type.Bool:
      return 'boolean'
    case Type.Timestamp:
    case Type.TimestampSecond:
    case Type.TimestampMillisecond:
    case Type.TimestampMicrosecond:
    case Type.TimestampNanosecond:
    case Type.Date:
    case Type.DateDay:
    case Type.DateMillisecond:
      return 'date'
    case Type.Null:
      return 'null'
    default:
      return 'unknown'
  }
}

// ---------------------------------------------------------------------------
// Arrow timestamp unit → divisor to convert stored int64 to milliseconds
// DuckDB encodes TIMESTAMP as microseconds (unit = 1e-6s) since epoch.
// ---------------------------------------------------------------------------

function timestampToISO(value: unknown, typeId: number): string {
  if (value === null || value === undefined) return ''
  let ms: number
  const raw = typeof value === 'bigint' ? value : BigInt(String(value))
  switch (typeId) {
    case Type.TimestampNanosecond:
      ms = Number(raw / 1_000_000n)
      break
    case Type.TimestampMicrosecond:
    case Type.Timestamp:
      ms = Number(raw / 1_000n)
      break
    case Type.TimestampMillisecond:
      ms = Number(raw)
      break
    case Type.TimestampSecond:
      ms = Number(raw) * 1000
      break
    // DateDay is days since epoch; DateMillisecond is ms since epoch
    case Type.DateDay:
      ms = Number(raw) * 86_400_000
      break
    case Type.DateMillisecond:
    case Type.Date:
      ms = Number(raw)
      break
    default:
      ms = Number(raw)
  }
  return new Date(ms).toISOString()
}

// ---------------------------------------------------------------------------
// Normalize a single Arrow cell value to a plain JS value safe for JSON
// ---------------------------------------------------------------------------

function normalizeCell(value: unknown, typeId: number): unknown {
  if (value === null || value === undefined) return null

  // Timestamps / dates → ISO string
  if (
    typeId === Type.Timestamp ||
    typeId === Type.TimestampSecond ||
    typeId === Type.TimestampMillisecond ||
    typeId === Type.TimestampMicrosecond ||
    typeId === Type.TimestampNanosecond ||
    typeId === Type.Date ||
    typeId === Type.DateDay ||
    typeId === Type.DateMillisecond
  ) {
    return timestampToISO(value, typeId)
  }

  // BigInt — coerce to number if within safe range, otherwise string
  if (typeof value === 'bigint') {
    if (value >= BigInt(Number.MIN_SAFE_INTEGER) && value <= BigInt(Number.MAX_SAFE_INTEGER)) {
      return Number(value)
    }
    return String(value)
  }

  // Leave strings, booleans, numbers, null as-is
  return value
}

// ---------------------------------------------------------------------------
// Map our TableMeta type strings to DuckDB read_json column types.
// Our schema uses TEXT as a synonym for VARCHAR — read_json wants VARCHAR.
// ---------------------------------------------------------------------------

function jsonReadType(t: string): string {
  if (t === 'TEXT') return 'VARCHAR'
  return t
}

// ---------------------------------------------------------------------------
// getDuckDB — lazy singleton
// ---------------------------------------------------------------------------

export function getDuckDB(): Promise<AsyncDuckDB> {
  if (dbPromise !== null) return dbPromise

  dbPromise = (async () => {
    const logger = new duckdb.ConsoleLogger(duckdb.LogLevel.WARNING)
    const bundle = await duckdb.selectBundle(BUNDLES)
    const worker = new Worker(bundle.mainWorker!, { type: 'module' })
    const db = new duckdb.AsyncDuckDB(logger, worker)
    await db.instantiate(bundle.mainModule, bundle.pthreadWorker)
    return db
  })()

  return dbPromise
}

// ---------------------------------------------------------------------------
// ensureLoaded — normalize + DROP/CREATE + ingest
// ---------------------------------------------------------------------------

export async function ensureLoaded(state: ProcessedState): Promise<void> {
  // Idempotent: skip if same state reference is already loaded
  if (state === loadedStateRef) return

  const db = await getDuckDB()
  const conn = await db.connect()

  try {
    // Drop and recreate every table in TABLE_METADATA order
    for (let i = 0; i < TABLE_METADATA.length; i++) {
      const meta = TABLE_METADATA[i]
      await conn.query(`DROP TABLE IF EXISTS ${meta.name}`)
      await conn.query(CREATE_STATEMENTS[i])
    }

    // Normalize state → flat row arrays
    const tables = normalize(state)

    // Ingest each table that has rows.
    //
    // We use `INSERT INTO <t> BY NAME SELECT * FROM read_json(...)` rather than
    // duckdb-wasm's `insertJSONFromPath` because the latter silently positional-
    // matches alphabetized source columns against schema-order target columns,
    // which causes cast errors (e.g. last_scanned TEXT -> number_of_brokers INTEGER).
    // `read_json` with explicit `columns={...}` types disables inference and
    // `BY NAME` matches by column name, eliminating both pitfalls.
    for (const meta of TABLE_METADATA) {
      const rows = tables[meta.name as keyof typeof tables] as unknown[]
      if (!Array.isArray(rows) || rows.length === 0) continue

      const fname = `${meta.name}.json`
      await db.registerFileText(fname, JSON.stringify(rows))

      const columnsSpec = meta.columns
        .map((c) => `'${c.name}': '${jsonReadType(c.type)}'`)
        .join(', ')

      await conn.query(
        `INSERT INTO ${meta.name} BY NAME ` +
          `SELECT * FROM read_json('${fname}', format='array', columns={${columnsSpec}})`,
      )
    }
  } finally {
    await conn.close()
  }

  loadedStateRef = state
}

// ---------------------------------------------------------------------------
// runQuery — execute SQL and return typed result
// ---------------------------------------------------------------------------

export async function runQuery(sql: string): Promise<QueryResult> {
  const t0 = performance.now()
  const db = await getDuckDB()
  const conn = await db.connect()

  try {
    const arrowTable = await conn.query(sql)
    await conn.close()

    const durationMs = performance.now() - t0

    // Build column descriptors
    const columns: QueryResultColumn[] = arrowTable.schema.fields.map((field) => {
      const typeId = field.type.typeId
      return {
        name: field.name,
        type: field.type.toString(),
        jsType: arrowTypeToJsType(typeId),
      }
    })

    // Build per-column typeId lookup for cell normalization
    const typeIds = arrowTable.schema.fields.map((f) => f.type.typeId)

    // Convert Arrow rows → plain JS objects
    const rows: Record<string, unknown>[] = arrowTable.toArray().map((row) => {
      const plain = row.toJSON() as Record<string, unknown>
      const normalized: Record<string, unknown> = {}
      for (let ci = 0; ci < columns.length; ci++) {
        const colName = columns[ci].name
        normalized[colName] = normalizeCell(plain[colName], typeIds[ci])
      }
      return normalized
    })

    return {
      columns,
      rows,
      rowCount: rows.length,
      durationMs,
      sql,
    }
  } catch (err: unknown) {
    // Close connection best-effort on error
    try {
      await conn.close()
    } catch {
      // ignore
    }
    const durationMs = performance.now() - t0
    const message = err instanceof Error ? err.message : String(err)
    throw new WorkbenchQueryError(message, sql, durationMs)
  }
}

// ---------------------------------------------------------------------------
// resetDB — drop all tables and clear ingested state ref
// ---------------------------------------------------------------------------

export async function resetDB(): Promise<void> {
  const db = await getDuckDB()
  const conn = await db.connect()
  try {
    for (const t of TABLE_METADATA) {
      await conn.query(`DROP TABLE IF EXISTS ${t.name}`)
    }
  } finally {
    await conn.close()
  }
  loadedStateRef = null
}

// ---------------------------------------------------------------------------
// listTables — schema browser source of truth (from TABLE_METADATA, not DuckDB)
// ---------------------------------------------------------------------------

export function listTables(): readonly TableMeta[] {
  return TABLE_METADATA
}

// ---------------------------------------------------------------------------
// HMR defensiveness — terminate worker on hot reload to avoid stale instances
// ---------------------------------------------------------------------------

if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    if (dbPromise) {
      dbPromise.then((db) => db.terminate()).catch(() => {})
    }
    dbPromise = null
    loadedStateRef = null
  })
}
