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

  // Build default items for the array
  const defaultItems = schemaRegistries.map((registry) => {
    const key = sanitizeUrlToKey(registry.url)
    const subjectNames = registry.subjects.map((subject) => subject.name)
    return {
      id: key,
      source_url: registry.url,
      migrate: true,
      subjects: subjectNames,
    }
  })

  // Build dependencies for subjects based on registry id
  const idDependencies: Record<string, unknown> = {}
  schemaRegistries.forEach((registry) => {
    const key = sanitizeUrlToKey(registry.url)
    const subjectNames = registry.subjects.map((subject) => subject.name)
    
    idDependencies[key] = {
      properties: {
        subjects: {
          type: 'array',
          items: {
            type: 'string',
            enum: subjectNames,
          },
          uniqueItems: true,
          title: 'Subjects',
        },
      },
    }
  })

  // Build schema with array structure
  const schema = {
    type: 'object',
    properties: {
      schema_registries: {
        type: 'array',
        title: 'Schema Registries',
        items: {
          type: 'object',
          properties: {
            id: {
              type: 'string',
              title: 'Schema Registry ID',
              readOnly: true,
              enum: schemaRegistries.map((r) => sanitizeUrlToKey(r.url)),
            },
            source_url: {
              type: 'string',
              title: 'Schema Registry URL',
              readOnly: true,
            },
            migrate: {
              type: 'boolean',
              title: 'Migrate this schema registry',
            },
            subjects: {
              type: 'array',
              items: {
                type: 'string',
              },
              uniqueItems: true,
              title: 'Subjects',
            },
          },
          required: ['id', 'migrate'],
          dependencies: {
            id: {
              oneOf: Object.entries(idDependencies).map(([registryId, dependency]) => ({
                properties: {
                  id: {
                    const: registryId,
                  },
                  ...(dependency as { properties: Record<string, unknown> }).properties,
                },
              })),
            },
            migrate: {
              oneOf: [
                {
                  properties: {
                    migrate: {
                      const: true,
                    },
                  },
                },
                {
                  properties: {
                    migrate: {
                      const: false,
                    },
                    subjects: {
                      type: 'array',
                      default: [],
                      readOnly: true,
                    },
                  },
                },
              ],
            },
          },
        },
        default: defaultItems,
      },
    },
    required: ['schema_registries'],
  }

  // Build UI schema for array items
  const uiSchema = {
    schema_registries: {
      'ui:options': {
        addable: false,
        orderable: false,
        removable: false,
      },
      'ui:title': 'Schema Registries',
      items: {
        'ui:title': '',
        'ui:order': ['source_url', 'id', 'migrate', 'subjects'],
        id: {
          'ui:widget': 'hidden',
        },
        source_url: {
          'ui:widget': 'text',
          'ui:readonly': true,
        },
        migrate: {
          'ui:widget': 'radio',
        },
        subjects: {
          'ui:widget': 'checkboxes',
          'ui:options': {
            inline: false,
          },
        },
      },
    },
  }

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
          schema,
          uiSchema,
        },
        on: {
          NEXT: {
            target: 'confirmation',
            actions: 'save_step_data',
          },
        },
      },
      confirmation: {
        meta: {
          title: 'Review Configuration',
          description: 'Review your configuration before generating migration scripts',
        },
        on: {
          CONFIRM: {
            target: 'complete',
          },
          BACK: {
            target: 'subject_selection',
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
