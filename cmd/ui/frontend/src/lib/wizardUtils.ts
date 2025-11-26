import type { WizardType } from '@/types'
import { WIZARD_TYPES } from '@/constants'

/**
 * Gets the display title for a wizard type
 * @param wizardType - The wizard type identifier
 * @returns The human-readable title for the wizard
 */
export const getWizardTitle = (wizardType: WizardType): string => {
  switch (wizardType) {
    case WIZARD_TYPES.TARGET_INFRA:
      return 'Create Target Infrastructure'
    case WIZARD_TYPES.MIGRATION_INFRA:
      return 'Create Migration Infrastructure'
    case WIZARD_TYPES.MIGRATION_SCRIPTS:
      return 'Create Migration Scripts'
    default:
      return 'Create Migration Scripts'
  }
}

/**
 * Gets the display title for wizard files modal
 * @param wizardType - The wizard type identifier
 * @returns The human-readable title for the wizard files
 */
export const getWizardFilesTitle = (wizardType: WizardType): string => {
  switch (wizardType) {
    case WIZARD_TYPES.TARGET_INFRA:
      return 'Target Infrastructure Files'
    case WIZARD_TYPES.MIGRATION_INFRA:
      return 'Migration Infrastructure Files'
    case WIZARD_TYPES.MIGRATION_SCRIPTS:
      return 'Migration Scripts Files'
    case WIZARD_TYPES.MIGRATE_SCHEMAS:
      return 'Schema Registry Migration Scripts'
    case WIZARD_TYPES.MIGRATE_TOPICS:
      return 'Mirror Topics Migration Scripts'
    case WIZARD_TYPES.MIGRATE_ACLS:
      return 'ACL Migration Scripts'
    default:
      return 'Migration Scripts Files'
  }
}
