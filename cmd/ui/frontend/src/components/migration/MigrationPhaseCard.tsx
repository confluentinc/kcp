import { Button } from '@/components/common/ui/button'
import { CheckCircle2, ArrowRight } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import type { WizardType } from '@/types'
import { WIZARD_TYPES } from '@/constants'

interface Phase {
  step: number
  id: WizardType
  title: string
  description: string
  icon: LucideIcon
  handler: () => void
}

interface MigrationPhaseCardProps {
  phase: Phase
  isCompleted: boolean
  onGenerate: () => void
  onView: () => void
  showConnector?: boolean
}

export const MigrationPhaseCard = ({
  phase,
  isCompleted,
  onGenerate,
  onView,
  showConnector = false,
}: MigrationPhaseCardProps) => {
  const Icon = phase.icon

  return (
    <>
      <div className="flex items-stretch flex-1">
        {/* Phase Card */}
        <div
          className={`flex-1 relative flex flex-col items-center p-6 rounded-lg border-2 transition-all bg-white dark:bg-card hover:border-gray-300 dark:hover:border-gray-600 h-full ${
            isCompleted ? 'border-accent' : 'border-gray-200 dark:border-border'
          }`}
        >
          {/* Step Number Badge */}
          <div
            className={`absolute -top-3 -left-3 w-8 h-8 rounded-full flex items-center justify-center font-bold text-sm border-2 ${
              isCompleted
                ? 'bg-white dark:bg-card text-gray-700 dark:text-gray-300 border-accent'
                : 'bg-white dark:bg-card text-gray-700 dark:text-gray-300 border-accent'
            }`}
          >
            {phase.step}
          </div>

          {/* Icon */}
          <div className="mb-4 p-3 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400">
            <Icon className="w-6 h-6" />
          </div>

          {/* Title */}
          <h4
            className={`text-lg font-semibold mb-2 text-center flex items-center gap-1.5 justify-center ${
              isCompleted ? 'text-accent' : 'text-gray-900 dark:text-gray-100'
            }`}
          >
            {phase.title}
            {isCompleted && (
              <CheckCircle2 className="w-4 h-4 text-green-500 dark:text-green-400 flex-shrink-0" />
            )}
          </h4>

          {/* Description */}
          <p className="text-xs text-gray-500 dark:text-gray-400 text-center mb-4">
            {phase.description}
          </p>

          {/* Action Buttons */}
          {isCompleted ? (
            <div className="flex gap-2 w-full">
              <Button
                variant="outline"
                size="sm"
                onClick={onGenerate}
                className="flex-1"
              >
                {phase.id === WIZARD_TYPES.MIGRATION_SCRIPTS
                  ? 'Generate Migration Assets'
                  : 'Generate Terraform'}
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={onView}
                className="flex-1"
              >
                View Terraform
              </Button>
            </div>
          ) : (
            <Button
              variant="outline"
              size="sm"
              onClick={onGenerate}
              className="w-auto"
            >
              {phase.id === WIZARD_TYPES.MIGRATION_SCRIPTS
                ? 'Generate Assets'
                : 'Generate Terraform'}
            </Button>
          )}
        </div>
      </div>

      {/* Connector Arrow */}
      {showConnector && (
        <div className="px-2 flex-shrink-0 flex items-center">
          <ArrowRight
            className={`w-5 h-5 ${
              isCompleted
                ? 'text-green-500 dark:text-green-600'
                : 'text-gray-300 dark:text-gray-600'
            }`}
          />
        </div>
      )}
    </>
  )
}
