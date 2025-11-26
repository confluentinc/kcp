import { useState } from 'react'
import { FileText, MessageSquare, Shield } from 'lucide-react'
import { Button } from '@/components/common/ui/button'
import { TerraformFileViewer } from './TerraformFileViewer'
import { WIZARD_TYPES } from '@/constants'
import type { WizardType, TerraformFiles } from '@/types'

interface MigrationScriptsFileViewerProps {
  clusterKey: string
  clusterName: string
  getTerraformFiles: (clusterKey: string, wizardType: WizardType) => TerraformFiles | null
}

interface ScriptType {
  id: WizardType
  title: string
  icon: typeof FileText
}

const SCRIPT_TYPES: ScriptType[] = [
  {
    id: WIZARD_TYPES.MIGRATE_TOPICS,
    title: 'Mirror Topics',
    icon: MessageSquare,
  },
  {
    id: WIZARD_TYPES.MIGRATE_SCHEMAS,
    title: 'Schema Registry',
    icon: FileText,
  },
  {
    id: WIZARD_TYPES.MIGRATE_ACLS,
    title: 'ACL Migration',
    icon: Shield,
  },
]

export const MigrationScriptsFileViewer = ({
  clusterKey,
  clusterName,
  getTerraformFiles,
}: MigrationScriptsFileViewerProps) => {
  // Filter to only show available script types
  const availableScriptTypes = SCRIPT_TYPES.filter((type) =>
    getTerraformFiles(clusterKey, type.id)
  )

  // Auto-select the first available type
  const [selectedScriptType, setSelectedScriptType] = useState<WizardType>(
    availableScriptTypes[0]?.id || WIZARD_TYPES.MIGRATE_TOPICS
  )

  if (availableScriptTypes.length === 0) {
    return (
      <p className="text-gray-600 dark:text-gray-400 p-6">
        No migration scripts generated yet.
      </p>
    )
  }

  const files = getTerraformFiles(clusterKey, selectedScriptType)

  return (
    <div className="flex flex-col h-full">
      {/* Tabs for switching between script types - only show if multiple types available */}
      {availableScriptTypes.length > 1 && (
        <div className="p-3 bg-white dark:bg-card border-b border-gray-200 dark:border-border flex-shrink-0">
          <div className="flex gap-2">
            {availableScriptTypes.map((type) => (
              <Button
                key={type.id}
                variant={selectedScriptType === type.id ? 'default' : 'outline'}
                size="sm"
                onClick={() => setSelectedScriptType(type.id)}
                className="text-xs"
              >
                <type.icon className="h-3 w-3 mr-1" />
                {type.title}
              </Button>
            ))}
          </div>
        </div>
      )}
      
      {/* File viewer using the existing TerraformFileViewer component */}
      <div className="flex-1 min-h-0">
        <TerraformFileViewer
          files={files}
          clusterName={clusterName}
          wizardType={selectedScriptType}
        />
      </div>
    </div>
  )
}

