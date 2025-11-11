import { FileText, MessageSquare, Shield } from 'lucide-react'
import { WIZARD_TYPES } from '@/constants'
import type { WizardType } from '@/types'

interface MigrationScriptsSelectionProps {
  onSelect: (wizardType: WizardType) => void
}

export const MigrationScriptsSelection = ({ onSelect }: MigrationScriptsSelectionProps) => {
  return (
    <div className="p-6">
      <p className="text-gray-600 dark:text-gray-400 mb-6">
        Choose the type of migration scripts you want to generate for this cluster.
      </p>
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <button
          onClick={() => onSelect(WIZARD_TYPES.MIGRATE_SCHEMAS)}
          className="flex flex-col items-center p-6 rounded-lg border-2 border-gray-200 dark:border-border bg-white dark:bg-card hover:border-accent hover:shadow-md transition-all cursor-pointer group"
        >
          <div className="mb-4 p-3 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 group-hover:bg-accent/10 group-hover:text-accent transition-colors">
            <FileText className="w-6 h-6" />
          </div>
          <h3 className="text-lg font-semibold mb-2 text-center text-gray-900 dark:text-gray-100 group-hover:text-accent transition-colors">
            Schema Registry Migration Scripts
          </h3>
          <p className="text-sm text-gray-500 dark:text-gray-400 text-center">
            Generate scripts to migrate schemas from MSK Schema Registry to Confluent Cloud
          </p>
        </button>
        <button
          onClick={() => onSelect(WIZARD_TYPES.MIGRATE_TOPICS)}
          className="flex flex-col items-center p-6 rounded-lg border-2 border-gray-200 dark:border-border bg-white dark:bg-card hover:border-accent hover:shadow-md transition-all cursor-pointer group"
        >
          <div className="mb-4 p-3 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 group-hover:bg-accent/10 group-hover:text-accent transition-colors">
            <MessageSquare className="w-6 h-6" />
          </div>
          <h3 className="text-lg font-semibold mb-2 text-center text-gray-900 dark:text-gray-100 group-hover:text-accent transition-colors">
            Topic Migration Scripts
          </h3>
          <p className="text-sm text-gray-500 dark:text-gray-400 text-center">
            Generate scripts to migrate topics from MSK to Confluent Cloud
          </p>
        </button>
        <button
          onClick={() => onSelect(WIZARD_TYPES.MIGRATE_ACLS)}
          className="flex flex-col items-center p-6 rounded-lg border-2 border-gray-200 dark:border-border bg-white dark:bg-card hover:border-accent hover:shadow-md transition-all cursor-pointer group"
        >
          <div className="mb-4 p-3 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 group-hover:bg-accent/10 group-hover:text-accent transition-colors">
            <Shield className="w-6 h-6" />
          </div>
          <h3 className="text-lg font-semibold mb-2 text-center text-gray-900 dark:text-gray-100 group-hover:text-accent transition-colors">
            ACL Migration Scripts
          </h3>
          <p className="text-sm text-gray-500 dark:text-gray-400 text-center">
            Generate scripts to migrate ACLs from MSK to Confluent Cloud
          </p>
        </button>
      </div>
    </div>
  )
}

