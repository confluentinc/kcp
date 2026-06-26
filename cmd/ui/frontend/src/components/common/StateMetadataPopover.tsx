import { Info } from 'lucide-react'
import { Button } from '@/components/common/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/common/ui/popover'
import { cn } from '@/lib/utils'
import { formatDate } from '@/lib/formatters'
import { useKcpState } from '@/stores/store'

interface StateMetadataPopoverProps {
  className?: string
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
    rows.push({ key: 'date', label: 'Build date', value: build.date })
  }
  if (state.timestamp) {
    rows.push({ key: 'created', label: 'Created', value: formatDate(state.timestamp) })
  }
  if (state.updated_at) {
    rows.push({ key: 'updated', label: 'Last updated', value: formatDate(state.updated_at) })
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
      <PopoverContent align="end" className="w-72">
        <p className="mb-2 text-sm font-semibold">State file</p>
        <div className="space-y-1 text-sm">
          {rows.map((row) => (
            <div key={row.key} className="flex justify-between gap-4">
              <span className="text-muted-foreground">{row.label}</span>
              <span className="break-all text-right font-mono" data-testid={`meta-${row.key}`}>
                {row.value}
              </span>
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}
