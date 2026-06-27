import { Info } from 'lucide-react'
import { Button } from '@/components/common/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/common/ui/popover'
import { cn } from '@/lib/utils'
import { formatDate } from '@/lib/formatters'
import { useKcpState } from '@/stores/store'

interface StateMetadataPopoverProps {
  className?: string
}

// formatMaybeDate formats a parseable date string, falling back to the raw
// value if it isn't a date (build_info.date is set at build time and may not
// always be RFC3339).
function formatMaybeDate(value: string): string {
  const formatted = formatDate(value)
  return formatted === 'Invalid Date' ? value : formatted
}

export function StateMetadataPopover({ className }: StateMetadataPopoverProps) {
  const state = useKcpState()
  if (!state) return null

  const build = state.kcp_build_info
  const rows: { key: string; label: string; value: string }[] = []

  rows.push({
    key: 'schema',
    label: 'Schema version',
    value: state.schema_version ? String(state.schema_version) : 'unversioned (legacy)',
  })
  if (build?.version) {
    rows.push({ key: 'build', label: 'KCP build', value: build.version })
  }
  if (build?.commit && build.commit !== 'unknown') {
    rows.push({ key: 'commit', label: 'Commit', value: build.commit })
  }
  if (build?.date && build.date !== 'unknown') {
    rows.push({ key: 'date', label: 'Build date', value: formatMaybeDate(build.date) })
  }
  if (state.timestamp) {
    rows.push({ key: 'created', label: 'Created', value: formatMaybeDate(state.timestamp) })
  }
  if (state.updated_at) {
    rows.push({ key: 'updated', label: 'Last updated', value: formatMaybeDate(state.updated_at) })
  }
  if (state.migrated_from) {
    rows.push({ key: 'migrated', label: 'Migrated from', value: state.migrated_from })
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label="State file metadata"
          data-testid="state-metadata-trigger"
          className={cn(className)}
        >
          <Info className="h-4 w-4" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80">
        <p className="mb-3 text-sm font-semibold">State file</p>
        <dl className="space-y-2.5 text-sm">
          {rows.map((row) => (
            <div key={row.key}>
              <dt className="text-xs text-muted-foreground">{row.label}</dt>
              <dd className="break-words font-mono" data-testid={`meta-${row.key}`}>
                {row.value}
              </dd>
            </div>
          ))}
        </dl>
      </PopoverContent>
    </Popover>
  )
}
