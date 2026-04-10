import { Server, Network, Code } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import type { WizardType } from '@/types'
import { WIZARD_TYPES } from '@/constants'
import { MigrationPhaseCard } from './MigrationPhaseCard'

interface Phase {
  step: number
  id: WizardType
  title: string
  description: string
  icon: LucideIcon
  handler: () => void
}

interface MigrationFlowProps {
  clusterKey: string
  clusterName: string
  getPhaseStatus: (clusterKey: string, wizardType: WizardType) => 'completed' | 'pending'
  onCreateTargetInfrastructure: () => void
  onCreateMigrationInfrastructure: () => void
  onCreateMigrationScripts: () => void
  onViewTerraform: (clusterKey: string, wizardType: WizardType, clusterName: string) => void
  migrationScriptsDescription?: string
}

export const MigrationFlow = ({
  clusterKey,
  clusterName,
  getPhaseStatus,
  onCreateTargetInfrastructure,
  onCreateMigrationInfrastructure,
  onCreateMigrationScripts,
  onViewTerraform,
  migrationScriptsDescription,
}: MigrationFlowProps) => {
  const phases: Phase[] = [
    {
      step: 1,
      id: WIZARD_TYPES.TARGET_INFRA,
      title: 'Confluent Cloud Infrastructure',
      description: 'Generate Terraform for Your Target Infrastructure',
      icon: Server,
      handler: onCreateTargetInfrastructure,
    },
    {
      step: 2,
      id: WIZARD_TYPES.MIGRATION_INFRA,
      title: 'Migration Infrastructure',
      description: 'Generate Terraform for Your Migration Infrastructure',
      icon: Network,
      handler: onCreateMigrationInfrastructure,
    },
    {
      step: 3,
      id: WIZARD_TYPES.MIGRATION_SCRIPTS,
      title: 'Migration Assets',
      description: migrationScriptsDescription || 'Generate Migration Assets to Move Data to Confluent Cloud',
      icon: Code,
      handler: onCreateMigrationScripts,
    },
  ]

  return (
    <div className="py-6 px-6">
      <div className="mb-6">
        <h3 className="text-lg font-semibold text-gray-700 dark:text-gray-300">Migration Steps</h3>
      </div>
      <div className="flex items-stretch justify-between gap-4">
        {phases.map((phase, index) => {
          const status = getPhaseStatus(clusterKey, phase.id)
          const isCompleted = status === 'completed'

          return (
            <MigrationPhaseCard
              key={phase.id}
              phase={phase}
              isCompleted={isCompleted}
              onGenerate={phase.handler}
              onView={() => onViewTerraform(clusterKey, phase.id, clusterName)}
              showConnector={index < phases.length - 1}
            />
          )
        })}
      </div>
    </div>
  )
}
