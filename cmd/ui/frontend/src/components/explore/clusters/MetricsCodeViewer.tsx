import { Button } from '@/components/common/ui/button'

interface MetricsCodeViewerProps {
  data: string
  label: string
  onCopy: () => void
  isJSON?: boolean
}

export const MetricsCodeViewer = ({
  data,
  label,
  onCopy,
  isJSON = false,
}: MetricsCodeViewerProps) => {
  return (
    <div className="space-y-4 min-w-0">
      <div className="bg-secondary rounded-lg p-4 min-w-0 max-w-full">
        <div className="flex items-center mb-2">
          <div className="flex-1" />
          <Button
            variant="outline"
            size="sm"
            onClick={onCopy}
            className="text-xs flex-shrink-0"
          >
            Copy {label}
          </Button>
        </div>
        <div className="w-full overflow-hidden">
          <pre
            className={`text-xs text-foreground overflow-auto max-h-96 bg-card p-4 rounded border max-w-full ${
              isJSON ? '' : 'font-mono'
            }`}
          >
            {data}
          </pre>
        </div>
      </div>
    </div>
  )
}


