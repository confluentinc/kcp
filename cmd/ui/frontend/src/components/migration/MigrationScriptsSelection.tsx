import { useState } from 'react'
import { FileText, MessageSquare, Shield, ArrowLeft, Image, Eye } from 'lucide-react'
import { Button } from '@/components/common/ui/button'
import { WIZARD_TYPES } from '@/constants'
import type { WizardType } from '@/types'
import {
    Wizard,
    createSchemaRegistryMigrationScriptsWizardConfig,
    createMirrorTopicsMigrationScriptsWizardConfig,
} from '@/components/migration/wizards'

interface MigrationScriptsSelectionProps {
    clusterArn: string
    onComplete: (wizardType: WizardType) => void
    onClose: () => void
    hasGeneratedFiles?: (wizardType: WizardType) => boolean
    onViewTerraform?: (wizardType: WizardType) => void
}

export const MigrationScriptsSelection = ({
    clusterArn,
    onComplete,
    onClose,
    hasGeneratedFiles,
    onViewTerraform,
}: MigrationScriptsSelectionProps) => {
    const [selectedWizardType, setSelectedWizardType] = useState<WizardType | null>(null)

    const handleCardClick = (wizardType: WizardType) => {
        setSelectedWizardType(wizardType)
    }

    const handleViewTerraform = (e: React.MouseEvent, wizardType: WizardType) => {
        e.stopPropagation() // Prevent card click
        if (onViewTerraform) {
            onViewTerraform(wizardType)
        }
    }

    const handleBackToSelection = () => {
        setSelectedWizardType(null)
    }

    const handleWizardComplete = () => {
        if (selectedWizardType) {
            onComplete(selectedWizardType)
        }
    }

    // If a wizard is selected, show that wizard or placeholder
    if (selectedWizardType) {
        // Show placeholder for ACLs for now
        if (selectedWizardType === WIZARD_TYPES.MIGRATE_ACLS) {
            return (
                <div className="relative flex flex-col items-center justify-center p-12 min-h-[400px]">
                    <button
                        onClick={handleBackToSelection}
                        className="absolute top-4 left-4 flex items-center gap-2 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 hover:text-gray-900 dark:hover:text-gray-100 transition-colors"
                    >
                        <ArrowLeft className="w-4 h-4" />
                        Back to Selection
                    </button>
                    <div className="mb-6 p-4 rounded-full bg-gray-100 dark:bg-gray-800">
                        <Image className="w-16 h-16 text-gray-400 dark:text-gray-500" />
                    </div>
                    <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-2">
                        ACL Migration Scripts
                    </h3>
                    <p className="text-gray-600 dark:text-gray-400 mb-6">
                        Coming soon - This wizard is under development
                    </p>
                </div>
            )
        }

        // Get the appropriate wizard config based on type
        const getWizardConfig = () => {
            switch (selectedWizardType) {
                case WIZARD_TYPES.MIGRATE_SCHEMAS:
                    return createSchemaRegistryMigrationScriptsWizardConfig()
                case WIZARD_TYPES.MIGRATE_TOPICS:
                    return createMirrorTopicsMigrationScriptsWizardConfig(clusterArn)
                default:
                    return createSchemaRegistryMigrationScriptsWizardConfig()
            }
        }

        // Show wizard for schema registry or mirror topics
        return (
            <div className="relative">
                <button
                    onClick={handleBackToSelection}
                    className="absolute top-0 left-0 flex items-center gap-2 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 hover:text-gray-900 dark:hover:text-gray-100 transition-colors z-10"
                >
                    <ArrowLeft className="w-4 h-4" />
                    Back to Selection
                </button>
                <Wizard
                    config={getWizardConfig()}
                    clusterKey={clusterArn}
                    wizardType={selectedWizardType}
                    onComplete={handleWizardComplete}
                    onClose={onClose}
                />
            </div>
        )
    }

    // Show selection cards
    return (
        <div className="p-6">
            <p className="text-gray-600 dark:text-gray-400 mb-6">
                Choose the type of migration scripts you want to generate for this cluster.
            </p>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <div className="relative">
                    <button
                        onClick={() => handleCardClick(WIZARD_TYPES.MIGRATE_ACLS)}
                        className="flex flex-col items-center p-6 rounded-lg border-2 border-gray-200 dark:border-border bg-white dark:bg-card hover:border-accent hover:shadow-md transition-all cursor-pointer group w-full h-full min-h-[240px]"
                    >
                        <div className="mb-4 p-3 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 group-hover:bg-accent/10 group-hover:text-accent transition-colors">
                            <Shield className="w-6 h-6" />
                        </div>
                        <h3 className="text-lg font-semibold mb-2 text-center text-gray-900 dark:text-gray-100 group-hover:text-accent transition-colors">
                            ACL Migration Scripts
                        </h3>
                        <p className="text-sm text-gray-500 dark:text-gray-400 text-center flex-1">
                            Generate scripts to migrate ACLs to Confluent Cloud
                        </p>
                        {hasGeneratedFiles && hasGeneratedFiles(WIZARD_TYPES.MIGRATE_ACLS) && (
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={(e) => handleViewTerraform(e, WIZARD_TYPES.MIGRATE_ACLS)}
                                className="text-xs mt-4"
                            >
                                <Eye className="h-3 w-3 mr-1" />
                                View Terraform
                            </Button>
                        )}
                    </button>
                </div>
                <div className="relative">
                    <button
                        onClick={() => handleCardClick(WIZARD_TYPES.MIGRATE_SCHEMAS)}
                        className="flex flex-col items-center p-6 rounded-lg border-2 border-gray-200 dark:border-border bg-white dark:bg-card hover:border-accent hover:shadow-md transition-all cursor-pointer group w-full h-full min-h-[240px]"
                    >
                        <div className="mb-4 p-3 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 group-hover:bg-accent/10 group-hover:text-accent transition-colors">
                            <FileText className="w-6 h-6" />
                        </div>
                        <h3 className="text-lg font-semibold mb-2 text-center text-gray-900 dark:text-gray-100 group-hover:text-accent transition-colors">
                            Schema Registry Migration Scripts
                        </h3>
                        <p className="text-sm text-gray-500 dark:text-gray-400 text-center flex-1">
                            Generate scripts to migrate schemas to Confluent Cloud
                        </p>
                        {hasGeneratedFiles && hasGeneratedFiles(WIZARD_TYPES.MIGRATE_SCHEMAS) && (
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={(e) => handleViewTerraform(e, WIZARD_TYPES.MIGRATE_SCHEMAS)}
                                className="text-xs mt-4"
                            >
                                <Eye className="h-3 w-3 mr-1" />
                                View Terraform
                            </Button>
                        )}
                    </button>
                </div>
                <div className="relative">
                    <button
                        onClick={() => handleCardClick(WIZARD_TYPES.MIGRATE_TOPICS)}
                        className="flex flex-col items-center p-6 rounded-lg border-2 border-gray-200 dark:border-border bg-white dark:bg-card hover:border-accent hover:shadow-md transition-all cursor-pointer group w-full h-full min-h-[240px]"
                    >
                        <div className="mb-4 p-3 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 group-hover:bg-accent/10 group-hover:text-accent transition-colors">
                            <MessageSquare className="w-6 h-6" />
                        </div>
                        <h3 className="text-lg font-semibold mb-2 text-center text-gray-900 dark:text-gray-100 group-hover:text-accent transition-colors">
                            Topic Migration Scripts
                        </h3>
                        <p className="text-sm text-gray-500 dark:text-gray-400 text-center flex-1">
                            Generate scripts to migrate topics to Confluent Cloud
                        </p>
                        {hasGeneratedFiles && hasGeneratedFiles(WIZARD_TYPES.MIGRATE_TOPICS) && (
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={(e) => handleViewTerraform(e, WIZARD_TYPES.MIGRATE_TOPICS)}
                                className="text-xs mt-4"
                            >
                                <Eye className="h-3 w-3 mr-1" />
                                View Terraform
                            </Button>
                        )}
                    </button>
                </div>
            </div>
        </div>
    )
}
