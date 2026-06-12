import { Download } from 'lucide-react'
import { Button } from '@/components/common/ui/button'

interface SummaryHeaderProps {
  onPrint: () => void
}

export const SummaryHeader = ({ onPrint }: SummaryHeaderProps) => {
  return (
    <div className="flex items-center justify-between">
      <div>
        <h1 className="text-2xl font-bold text-foreground">All Regions Cost Overview</h1>
      </div>
      <div className="flex items-center gap-4">
        <Button
          onClick={onPrint}
          variant="outline"
          size="sm"
        >
          <Download className="h-4 w-4 mr-2" />
          Export PDF
        </Button>
      </div>
    </div>
  )
}

