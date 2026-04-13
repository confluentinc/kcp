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
          className={`flex-1 relative flex flex-col items-center p-6 rounded-lg border shadow-sm transition-all bg-card hover:shadow-md h-full ${
            isCompleted ? 'border-success/50' : 'border-border'
          }`}
        >
          {/* Step Number Badge */}
          <div
            className={`absolute -top-3 -left-3 w-8 h-8 rounded-full flex items-center justify-center font-bold text-sm ${
              isCompleted
                ? 'bg-success text-white'
                : 'bg-accent text-white'
            }`}
          >
            {isCompleted ? (
              <CheckCircle2 className="w-5 h-5" />
            ) : (
              phase.step
            )}
          </div>

          {/* Icon */}
          <div className="mb-4 p-3 rounded-full bg-accent/10 text-accent">
            <Icon className="w-6 h-6" />
          </div>

          {/* Title */}
          <h4 className="text-lg font-semibold mb-2 text-center text-foreground">
            {phase.title}
          </h4>

          {/* Description */}
          <p className="text-xs text-muted-foreground text-center mb-4">
            {phase.description}
          </p>

          {/* Action Buttons */}
          {isCompleted ? (
            phase.id === WIZARD_TYPES.MIGRATION_SCRIPTS ? (
              <Button
                variant="default"
                size="sm"
                onClick={onGenerate}
                className="w-auto"
              >
                Generate Migration Assets
              </Button>
            ) : (
              <div className="flex gap-2 w-full">
                <Button
                  variant="default"
                  size="sm"
                  onClick={onGenerate}
                  className="flex-1"
                >
                  Generate Terraform
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
            )
          ) : (
            <Button
              variant="default"
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
          <ArrowRight className="w-5 h-5 text-border" />
        </div>
      )}
    </>
  )
}
