import { useState, useMemo, useEffect, useRef } from 'react'
import { format } from 'date-fns'
import {
  ResponsiveContainer,
  LineChart,
  Line,
  BarChart,
  Bar,
  AreaChart,
  Area,
  ScatterChart,
  Scatter,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from 'recharts'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/common/ui/select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/common/ui/popover'
import { Button } from '@/components/common/ui/button'
import type { QueryResult, QueryResultColumn } from '@/lib/duckdb/schema'

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export interface ResultsChartProps {
  result: QueryResult | null
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ChartType = 'Line' | 'Bar' | 'Area' | 'Scatter'

// ---------------------------------------------------------------------------
// Color helper — cycles through CSS chart vars --chart-1..--chart-5
// ---------------------------------------------------------------------------

function color(i: number): string {
  return `hsl(var(--chart-${(i % 5) + 1}))`
}

// ---------------------------------------------------------------------------
// Helper: is a column numeric?
// ---------------------------------------------------------------------------

function isNumeric(col: QueryResultColumn): boolean {
  return col.jsType === 'number' || col.jsType === 'bigint'
}

function isDate(col: QueryResultColumn): boolean {
  return col.jsType === 'date'
}

function toNumber(v: unknown): number | null {
  if (v === null || v === undefined) return null
  if (typeof v === 'number') return isFinite(v) ? v : null
  if (typeof v === 'bigint') {
    const n = Number(v)
    return isFinite(n) ? n : null
  }
  const n = Number(v)
  return isFinite(n) ? n : null
}

// ---------------------------------------------------------------------------
// Multi-select via Popover + checkboxes (no external multi-select component)
// ---------------------------------------------------------------------------

interface MultiSelectProps {
  options: string[]
  selected: string[]
  onChange: (next: string[]) => void
  label: string
}

function MultiSelect({ options, selected, onChange, label }: MultiSelectProps) {
  const toggle = (val: string) => {
    if (selected.includes(val)) {
      onChange(selected.filter((s) => s !== val))
    } else {
      onChange([...selected, val])
    }
  }

  const displayLabel =
    selected.length === 0
      ? 'None selected'
      : selected.length === options.length
      ? 'All'
      : selected.length === 1
      ? selected[0]
      : `${selected.length} selected`

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="h-8 text-xs gap-1 font-normal min-w-[120px] justify-between">
          <span className="truncate max-w-[140px]">{displayLabel}</span>
          <span className="opacity-50 shrink-0">▾</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-56 p-2">
        <p className="text-xs text-muted-foreground mb-2 px-1">{label}</p>
        <div className="max-h-48 overflow-y-auto space-y-1">
          {options.map((opt) => (
            <label
              key={opt}
              className="flex items-center gap-2 px-1 py-1 rounded hover:bg-secondary cursor-pointer text-sm"
            >
              <input
                type="checkbox"
                checked={selected.includes(opt)}
                onChange={() => toggle(opt)}
                className="h-3.5 w-3.5 accent-primary"
              />
              <span className="truncate font-mono text-xs">{opt}</span>
            </label>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}

// ---------------------------------------------------------------------------
// Controls bar
// ---------------------------------------------------------------------------

interface ChartControlsProps {
  columns: QueryResultColumn[]
  chartType: ChartType
  xCol: string
  yCols: string[]
  groupBy: string
  onChartType: (t: ChartType) => void
  onXCol: (c: string) => void
  onYCols: (cs: string[]) => void
  onGroupBy: (c: string) => void
}

function ChartControls({
  columns,
  chartType,
  xCol,
  yCols,
  groupBy,
  onChartType,
  onXCol,
  onYCols,
  onGroupBy,
}: ChartControlsProps) {
  const numericCols = columns.filter(isNumeric).map((c) => c.name)
  const nonNumericCols = columns.filter((c) => !isNumeric(c)).map((c) => c.name)
  const allColNames = columns.map((c) => c.name)

  return (
    <div className="flex flex-wrap items-center gap-2 px-3 py-2 border-b border-border bg-secondary/40 shrink-0">
      {/* Chart type */}
      <div className="flex items-center gap-1.5">
        <span className="text-xs text-muted-foreground">Type</span>
        <Select value={chartType} onValueChange={(v) => onChartType(v as ChartType)}>
          <SelectTrigger size="sm" className="h-8 text-xs w-24">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(['Bar', 'Line', 'Area', 'Scatter'] as ChartType[]).map((t) => (
              <SelectItem key={t} value={t} className="text-xs">
                {t}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {/* X axis */}
      <div className="flex items-center gap-1.5">
        <span className="text-xs text-muted-foreground">X</span>
        <Select value={xCol} onValueChange={onXCol}>
          <SelectTrigger size="sm" className="h-8 text-xs w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {allColNames.map((n) => (
              <SelectItem key={n} value={n} className="text-xs font-mono">
                {n}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {/* Y axis — multi-select */}
      <div className="flex items-center gap-1.5">
        <span className="text-xs text-muted-foreground">Y</span>
        <MultiSelect
          options={numericCols}
          selected={yCols}
          onChange={onYCols}
          label="Select Y axis columns"
        />
      </div>

      {/* Group by */}
      <div className="flex items-center gap-1.5">
        <span className="text-xs text-muted-foreground">Group by</span>
        <Select value={groupBy} onValueChange={onGroupBy}>
          <SelectTrigger size="sm" className="h-8 text-xs w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__none__" className="text-xs">
              None
            </SelectItem>
            {nonNumericCols.map((n) => (
              <SelectItem key={n} value={n} className="text-xs font-mono">
                {n}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Data transformation
// ---------------------------------------------------------------------------

function buildChartData(
  rows: Record<string, unknown>[],
  xCol: string,
  yCols: string[],
  groupBy: string,
  isDateX: boolean
): { data: Record<string, unknown>[]; seriesKeys: string[] } {
  if (groupBy === '__none__') {
    // No grouping — rows map 1:1
    const data: Record<string, unknown>[] = []
    for (const row of rows) {
      const xVal = row[xCol]
      if (xVal === null || xVal === undefined) continue
      const entry: Record<string, unknown> = {}
      entry[xCol] = isDateX ? Date.parse(String(xVal)) : xVal
      for (const y of yCols) {
        const n = toNumber(row[y])
        if (n !== null) entry[y] = n
      }
      data.push(entry)
    }
    return { data, seriesKeys: yCols }
  }

  // Grouped — pivot by groupBy value
  const seriesKeySet = new Set<string>()
  const byX = new Map<unknown, Record<string, unknown>>()

  for (const row of rows) {
    const xVal = row[xCol]
    if (xVal === null || xVal === undefined) continue
    const groupVal = String(row[groupBy] ?? '')
    const xKey = isDateX ? Date.parse(String(xVal)) : xVal

    if (!byX.has(xKey)) {
      byX.set(xKey, { [xCol]: xKey })
    }
    const entry = byX.get(xKey)!

    for (const y of yCols) {
      const seriesKey = `${y}·${groupVal}`
      seriesKeySet.add(seriesKey)
      const n = toNumber(row[y])
      if (n !== null) entry[seriesKey] = n
    }
  }

  const data = Array.from(byX.values()).sort((a, b) => {
    const av = a[xCol]
    const bv = b[xCol]
    if (typeof av === 'number' && typeof bv === 'number') return av - bv
    return String(av).localeCompare(String(bv))
  })

  return { data, seriesKeys: Array.from(seriesKeySet) }
}

// ---------------------------------------------------------------------------
// X-axis tick formatter
// ---------------------------------------------------------------------------

function makeTickFormatter(isDateX: boolean): ((value: unknown) => string) | undefined {
  if (!isDateX) return undefined
  return (value: unknown) => {
    try {
      return format(new Date(Number(value)), 'MM/dd HH:mm')
    } catch {
      return String(value)
    }
  }
}

// ---------------------------------------------------------------------------
// Chart renderer
// ---------------------------------------------------------------------------

interface ChartRendererProps {
  chartType: ChartType
  data: Record<string, unknown>[]
  xCol: string
  seriesKeys: string[]
  isDateX: boolean
  isNumericX: boolean
  groupBy: string
}

function ChartRenderer({
  chartType,
  data,
  xCol,
  seriesKeys,
  isDateX,
  isNumericX,
  groupBy,
}: ChartRendererProps) {
  const xAxisType: 'number' | 'category' = isDateX || isNumericX ? 'number' : 'category'
  const xAxisScale = isDateX ? 'time' : undefined
  const tickFormatter = makeTickFormatter(isDateX)

  const commonAxisProps = {
    dataKey: xCol,
    type: xAxisType,
    scale: xAxisScale,
    tickFormatter: tickFormatter,
  } as const

  const grid = <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" />
  const xAxis = <XAxis {...commonAxisProps} tick={{ fontSize: 11 }} />
  const yAxis = <YAxis tick={{ fontSize: 11 }} width={60} />
  const tooltip = <Tooltip />
  const legend = <Legend wrapperStyle={{ fontSize: 11 }} />

  if (chartType === 'Line') {
    return (
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
          {grid}
          {xAxis}
          {yAxis}
          {tooltip}
          {legend}
          {seriesKeys.map((key, i) => (
            <Line
              key={key}
              type="monotone"
              dataKey={key}
              stroke={color(i)}
              dot={false}
              isAnimationActive={false}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
    )
  }

  if (chartType === 'Bar') {
    return (
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
          {grid}
          {xAxis}
          {yAxis}
          {tooltip}
          {legend}
          {seriesKeys.map((key, i) => (
            <Bar key={key} dataKey={key} fill={color(i)} isAnimationActive={false} />
          ))}
        </BarChart>
      </ResponsiveContainer>
    )
  }

  if (chartType === 'Area') {
    return (
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
          {grid}
          {xAxis}
          {yAxis}
          {tooltip}
          {legend}
          {seriesKeys.map((key, i) => (
            <Area
              key={key}
              type="monotone"
              dataKey={key}
              stroke={color(i)}
              fill={color(i)}
              fillOpacity={0.25}
              isAnimationActive={false}
            />
          ))}
        </AreaChart>
      </ResponsiveContainer>
    )
  }

  // Scatter — only single series; warn if groupBy is set
  const scatterKey = seriesKeys[0]
  const scatterData = data.map((row) => ({
    x: row[xCol],
    y: scatterKey ? row[scatterKey] : undefined,
  }))

  return (
    <>
      {groupBy !== '__none__' && (
        <p className="text-xs text-muted-foreground px-3 pb-1">
          Note: Scatter chart shows only the first series when Group by is active.
        </p>
      )}
      <ResponsiveContainer width="100%" height="100%">
        <ScatterChart margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
          {grid}
          <XAxis dataKey="x" type={xAxisType} name={xCol} tick={{ fontSize: 11 }} tickFormatter={tickFormatter} />
          <YAxis dataKey="y" tick={{ fontSize: 11 }} width={60} />
          {tooltip}
          {legend}
          <Scatter name={scatterKey ?? ''} data={scatterData} fill={color(0)} isAnimationActive={false} />
        </ScatterChart>
      </ResponsiveContainer>
    </>
  )
}

// ---------------------------------------------------------------------------
// Main export
// ---------------------------------------------------------------------------

const MAX_POINTS = 5000

export function ResultsChart({ result }: ResultsChartProps) {
  // Determine defaults based on result
  const defaultXCol = useMemo(() => {
    if (!result) return ''
    const dateCol = result.columns.find(isDate)
    if (dateCol) return dateCol.name
    return result.columns[0]?.name ?? ''
  }, [result])

  const defaultChartType = useMemo<ChartType>(() => {
    if (!result) return 'Bar'
    const hasDateX = result.columns.some(isDate)
    return hasDateX ? 'Line' : 'Bar'
  }, [result])

  const defaultYCols = useMemo(() => {
    if (!result) return []
    const numCols = result.columns.filter(isNumeric).filter((c) => c.name !== defaultXCol)
    return numCols.slice(0, 4).map((c) => c.name)
  }, [result, defaultXCol])

  const [chartType, setChartType] = useState<ChartType>(defaultChartType)
  const [xCol, setXCol] = useState<string>(defaultXCol)
  const [yCols, setYCols] = useState<string[]>(defaultYCols)
  const [groupBy, setGroupBy] = useState<string>('__none__')

  // Re-sync defaults when result identity changes (new query executed)
  const prevResultRef = useRef(result)
  useEffect(() => {
    if (prevResultRef.current !== result) {
      prevResultRef.current = result
      setChartType(defaultChartType)
      setXCol(defaultXCol)
      setYCols(defaultYCols)
      setGroupBy('__none__')
    }
  }, [result, defaultChartType, defaultXCol, defaultYCols])

  if (!result) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
        Run a query to see a chart.
      </div>
    )
  }

  const numericColumns = result.columns.filter(isNumeric)
  if (numericColumns.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
        Pick a query that returns at least one numeric column.
      </div>
    )
  }

  const effectiveYCols = yCols.filter((y) => y !== xCol && numericColumns.some((c) => c.name === y))
  if (effectiveYCols.length === 0) {
    return (
      <div className="flex flex-col h-full">
        <ChartControls
          columns={result.columns}
          chartType={chartType}
          xCol={xCol}
          yCols={yCols}
          groupBy={groupBy}
          onChartType={setChartType}
          onXCol={setXCol}
          onYCols={setYCols}
          onGroupBy={setGroupBy}
        />
        <div className="flex items-center justify-center flex-1 text-muted-foreground text-sm">
          Pick numeric columns for the Y axis.
        </div>
      </div>
    )
  }

  const xColMeta = result.columns.find((c) => c.name === xCol)
  const isDateX = xColMeta ? isDate(xColMeta) : false
  const isNumericX = xColMeta ? isNumeric(xColMeta) : false

  const cappedRows =
    result.rows.length > MAX_POINTS ? result.rows.slice(0, MAX_POINTS) : result.rows
  const exceeds = result.rows.length > MAX_POINTS

  const { data, seriesKeys } = useMemo(
    () => buildChartData(cappedRows, xCol, effectiveYCols, groupBy, isDateX),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [cappedRows, xCol, effectiveYCols.join(','), groupBy, isDateX]
  )

  return (
    <div className="flex flex-col h-full">
      <ChartControls
        columns={result.columns}
        chartType={chartType}
        xCol={xCol}
        yCols={yCols}
        groupBy={groupBy}
        onChartType={setChartType}
        onXCol={setXCol}
        onYCols={setYCols}
        onGroupBy={setGroupBy}
      />
      {exceeds && (
        <div className="px-3 py-1.5 bg-amber-50 dark:bg-amber-950/30 text-amber-700 dark:text-amber-400 text-xs border-b border-border">
          Showing first {MAX_POINTS.toLocaleString()} of {result.rowCount.toLocaleString()} rows —
          add LIMIT to your query for a sharper chart.
        </div>
      )}
      <div className="flex-1 min-h-0 p-2">
        <ChartRenderer
          chartType={chartType}
          data={data}
          xCol={xCol}
          seriesKeys={seriesKeys}
          isDateX={isDateX}
          isNumericX={isNumericX}
          groupBy={groupBy}
        />
      </div>
    </div>
  )
}
