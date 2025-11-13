import type { WizardConfig } from './types'
import type { SchemaRegistry } from '@/types/api/state'
import { getAllSchemaRegistries } from '@/stores/store'

/**
 * Sanitize a URL to be a valid JSON schema property key
 * Example: "http://localhost:8081" -> "http_localhost_8081"
 */
function sanitizeUrlToKey(url: string): string {
  return url
    .replace(/:\/\//g, '_') // Replace :// with _
    .replace(/:/g, '_') // Replace remaining colons (ports) with _
    .replace(/\//g, '_') // Replace slashes with underscores
    .replace(/[^a-zA-Z0-9_]/g, '_') // Replace any other non-alphanumeric chars
}

export const createSchemaRegistryMigrationScriptsWizardConfig = (): WizardConfig => {
  const schemaRegistries: SchemaRegistry[] = getAllSchemaRegistries()

  // Build schema properties dynamically from schema registries
  const properties: Record<string, unknown> = {}
  const uiSchema: Record<string, unknown> = {}

  schemaRegistries.forEach((registry) => {
    const key = sanitizeUrlToKey(registry.url)
    const subjectNames = registry.subjects.map((subject) => subject.name)

    properties[key] = {
      type: 'object',
      title: `Schema Registry: ${registry.url}`,
      properties: {
        subjects: {
          type: 'array',
          items: {
            type: 'string',
            enum: subjectNames,
          },
          uniqueItems: true,
          title: 'Subjects',
          default: subjectNames,
        },
      },
    }

    uiSchema[key] = {
      subjects: {
        'ui:widget': 'checkboxes',
        'ui:options': {
          inline: false,
        },
      },
    }

  })

  return {
    id: 'schema-migration-scripts-wizard',
    title: 'Schema Migration Scripts Wizard',
    description: 'Select subjects from each schema registry to migrate',
    apiEndpoint: '/assets/migration-scripts/schemas',
    initial: 'subject_selection',

    states: {
      subject_selection: {
        meta: {
          title: 'Select Subjects',
          description: 'Select subjects from each schema registry that you want to migrate',
          schema: {
            type: 'object',
            properties,
          },
          uiSchema,
        },
        on: {
          NEXT: {
            target: 'complete',
            actions: 'save_step_data',
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
