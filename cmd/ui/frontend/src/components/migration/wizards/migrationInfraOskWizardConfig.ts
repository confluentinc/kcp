import type { WizardConfig } from './types'

/**
 * Stub wizard config for OSK migration infrastructure.
 * This will be replaced with a full implementation in a future task.
 */
export const createMigrationInfraOskWizardConfig = (clusterKey: string): WizardConfig => ({
  id: 'migration-infra-osk-wizard',
  title: 'OSK Migration Infrastructure Wizard',
  description: `Configure migration infrastructure for OSK cluster ${clusterKey}`,
  apiEndpoint: '/assets/migration',
  initial: 'placeholder',
  states: {
    placeholder: {
      meta: {
        title: 'OSK Migration Infrastructure - Coming Soon',
        description: 'This wizard is being implemented.',
        schema: { type: 'object', properties: {} },
        uiSchema: {},
      },
      on: {},
    },
  },
  guards: {},
  actions: {},
})
