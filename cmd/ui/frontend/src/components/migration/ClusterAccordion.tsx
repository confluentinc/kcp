import { ChevronRight } from 'lucide-react'
import type { ReactNode } from 'react'

interface ClusterAccordionProps {
  clusterName: string
  isExpanded: boolean
  onToggle: () => void
  children: ReactNode
}

export const ClusterAccordion = ({
  clusterName,
  isExpanded,
  onToggle,
  children,
}: ClusterAccordionProps) => {
  return (
    <div
      className={`bg-card rounded-lg border overflow-hidden transition-all ${
        isExpanded
          ? 'border-accent/50 shadow-md border-l-[3px] border-l-accent'
          : 'border-border'
      }`}
    >
      {/* Cluster Header Row - Clickable */}
      <div
        className={`px-6 py-4 border-b border-border cursor-pointer transition-colors duration-150 ${
          isExpanded ? 'bg-accent/5' : 'hover:bg-secondary'
        }`}
        onClick={onToggle}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center space-x-3">
            <ChevronRight
              className={`h-4 w-4 text-muted-foreground transition-transform duration-200 ${
                isExpanded ? 'rotate-90' : ''
              }`}
            />
            <h3 className="text-lg font-medium text-foreground">
              {clusterName}
            </h3>
          </div>
        </div>
      </div>

      {/* Content - Only shown when expanded */}
      {isExpanded && (
        <div className="border-t border-border bg-secondary/30 overflow-visible pt-4">
          {children}
        </div>
      )}
    </div>
  )
}
