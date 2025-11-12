import type { WizardConfig } from './types'
import type { SchemaRegistry } from '@/types/api/state'
import { getAllSchemaRegistries } from '@/stores/store'

export const createSchemaMigrationScriptsWizardConfig = (): WizardConfig => {
  const schemaRegistries: SchemaRegistry[] = getAllSchemaRegistries()
  
  // Extract URLs for the dropdown
  const schemaRegistryUrls = schemaRegistries.map((registry) => registry.url)

  return {
    id: 'schema-migration-scripts-wizard',
    title: 'Schema Migration Scripts Wizard',
    description: 'Generate schema migration scripts and automation tools',
    apiEndpoint: '/assets/migration-scripts/schemas',
    initial: 'schema_registry_selection',

    states: {
      schema_registry_selection: {
        meta: {
          title: 'Select Schema Registry',
          description: 'Which schema registry do you wish to target?',
          schema: {
            type: 'object',
            properties: {
              schema_registry_url: {
                type: 'string',
                enum: schemaRegistryUrls,
                title: 'Schema Registry URL',
                description: 'Select the schema registry you want to target',
              },
            },
            required: ['schema_registry_url'],
          },
          uiSchema: {
            schema_registry_url: {
              'ui:widget': 'select',
              'ui:placeholder': 'Select a schema registry...',
            },
          },
        },
        on: {
          NEXT: {
            target: 'schema_registry_info',
            actions: 'save_step_data',
          },
        },
      },
      schema_registry_info: {
        meta: {
          title: 'Schema Registry Information',
          description: 'Selected schema registry details',
          schema: {
            type: 'object',
            properties: {
              selected_registry_info: {
                type: 'string',
                title: 'Registry Information',
                description: 'Details of the selected schema registry',
                readOnly: true,
              },
            },
          },
          uiSchema: {
            selected_registry_info: {
              'ui:widget': 'textarea',
              'ui:options': {
                rows: 15,
              },
            },
          },
        },
        on: {
          NEXT: {
            target: 'complete',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'schema_registry_selection',
            actions: 'undo_save_step_data',
          },
        },
      },
      complete: {
        type: 'final',
        meta: {
          title: 'Configuration Complete',
          message: 'Your migration scripts configuration is ready to be processed...',
        },
      },
    },

    guards: {},

    actions: {
      save_step_data: 'save_step_data',
      undo_save_step_data: 'undo_save_step_data',
    },
  }
}
