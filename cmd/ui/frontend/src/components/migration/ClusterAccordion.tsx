import { ChevronDown, ChevronRight } from 'lucide-react'
import type { Cluster } from '@/types'
import type { ReactNode } from 'react'

interface ClusterAccordionProps {
  cluster: Cluster
  isExpanded: boolean
  onToggle: () => void
  children: ReactNode
}

export const ClusterAccordion = ({
  cluster,
  isExpanded,
  onToggle,
  children,
}: ClusterAccordionProps) => {
  return (
    <div
      className={`bg-white dark:bg-card rounded-lg border overflow-hidden transition-all ${
        isExpanded
          ? 'border-accent shadow-md dark:border-accent'
          : 'border-gray-200 dark:border-border'
      }`}
    >
      {/* Cluster Header Row - Clickable */}
      <div
        className={`px-6 py-4 border-b border-gray-200 dark:border-border cursor-pointer transition-colors ${
          isExpanded
            ? 'bg-accent/5 dark:bg-accent/10'
            : 'hover:bg-gray-50 dark:hover:bg-gray-700'
        }`}
        onClick={onToggle}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center space-x-6">
            <div className="flex items-center space-x-3">
              {isExpanded ? (
                <ChevronDown className="h-4 w-4 text-gray-400 dark:text-gray-500" />
              ) : (
                <ChevronRight className="h-4 w-4 text-gray-400 dark:text-gray-500" />
              )}
              <h3 className="text-lg font-medium text-gray-900 dark:text-gray-100">
                {cluster.name}
              </h3>
            </div>
          </div>
        </div>
      </div>

      {/* Content - Only shown when expanded */}
      {isExpanded && (
        <div className="border-t border-gray-200 dark:border-border bg-gray-50 dark:bg-card overflow-visible pt-4">
          {children}
        </div>
      )}
    </div>
  )
}

