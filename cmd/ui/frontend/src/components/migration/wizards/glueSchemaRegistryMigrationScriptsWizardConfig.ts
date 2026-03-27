import type { WizardConfig } from './types'
import type { GlueSchemaRegistry } from '@/types/api/state'
import { getAllGlueSchemaRegistries } from '@/stores/store'

/**
 * Sanitize a registry name to be a valid JSON schema property key
 */
function sanitizeRegistryToKey(registryName: string, region: string): string {
  return `${registryName}_${region}`.replace(/[^a-zA-Z0-9_]/g, '_')
}

export const createGlueSchemaRegistryMigrationScriptsWizardConfig = (): WizardConfig => {
  const glueRegistries: GlueSchemaRegistry[] = getAllGlueSchemaRegistries()

  // Build default items for the array
  const defaultItems = glueRegistries.map((registry) => {
    const key = sanitizeRegistryToKey(registry.registry_name, registry.region)
    const schemaNames = registry.schemas.map((s) => s.schema_name)
    return {
      id: key,
      source_label: `${registry.registry_name} (${registry.region})`,
      registry_name: registry.registry_name,
      region: registry.region,
      migrate: true,
      schema_names: schemaNames,
      schemas: registry.schemas,
    }
  })

  // Build dependencies for schema_names based on registry id
  const idDependencies: Record<string, unknown> = {}
  glueRegistries.forEach((registry) => {
    const key = sanitizeRegistryToKey(registry.registry_name, registry.region)
    const schemaNames = registry.schemas.map((s) => s.schema_name)

    idDependencies[key] = {
      properties: {
        schema_names: {
          type: 'array',
          items: {
            type: 'string',
            enum: schemaNames,
          },
          uniqueItems: true,
          title: 'Schemas',
        },
      },
    }
  })

  // Build schema with array structure
  const schema = {
    type: 'object',
    properties: {
      glue_registries: {
        type: 'array',
        title: 'AWS Glue Schema Registries',
        items: {
          type: 'object',
          properties: {
            id: {
              type: 'string',
              title: 'Registry ID',
              readOnly: true,
              enum: glueRegistries.map((r) => sanitizeRegistryToKey(r.registry_name, r.region)),
            },
            source_label: {
              type: 'string',
              title: 'Glue Schema Registry',
              readOnly: true,
            },
            registry_name: {
              type: 'string',
            },
            region: {
              type: 'string',
            },
            migrate: {
              type: 'boolean',
              title: 'Migrate this registry',
            },
            schema_names: {
              type: 'array',
              items: {
                type: 'string',
              },
              uniqueItems: true,
              title: 'Schemas',
            },
            schemas: {
              type: 'array',
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
                    schema_names: {
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
    required: ['glue_registries'],
  }

  // Build UI schema for array items
  const uiSchema = {
    glue_registries: {
      'ui:options': {
        addable: false,
        orderable: false,
        removable: false,
      },
      'ui:title': 'AWS Glue Schema Registries',
      items: {
        'ui:title': '',
        'ui:order': ['source_label', 'id', 'registry_name', 'region', 'migrate', 'schema_names', 'schemas'],
        id: {
          'ui:widget': 'hidden',
        },
        source_label: {
          'ui:widget': 'text',
          'ui:readonly': true,
        },
        registry_name: {
          'ui:widget': 'hidden',
        },
        region: {
          'ui:widget': 'hidden',
        },
        migrate: {
          'ui:widget': 'radio',
        },
        schema_names: {
          'ui:widget': 'checkboxes',
          'ui:options': {
            inline: false,
          },
        },
        schemas: {
          'ui:widget': 'hidden',
        },
      },
    },
  }

  return {
    id: 'glue-schema-migration-scripts-wizard',
    title: 'Glue Schema Migration Scripts Wizard',
    description: 'Generate Terraform to migrate AWS Glue schemas to Confluent Cloud',
    apiEndpoint: '/assets/migration-scripts/glue-schemas',
    initial: 'confluent_cloud_schema_registry_url',

    states: {
      confluent_cloud_schema_registry_url: {
        meta: {
          title: 'Confluent Cloud Schema Registry URL',
          description: 'Enter the URL for your Confluent Cloud schema registry',
          schema: {
            type: 'object',
            properties: {
              confluent_cloud_schema_registry_url: {
                type: 'string',
                title: 'Confluent Cloud Schema Registry URL',
                format: 'uri',
              },
            },
            required: ['confluent_cloud_schema_registry_url'],
          },
          uiSchema: {
            confluent_cloud_schema_registry_url: {
              'ui:placeholder': 'e.g., https://psrc-xxxxx.us-east-2.aws.confluent.cloud',
            },
          },
        },
        on: {
          NEXT: {
            target: 'registry_selection',
            actions: 'save_step_data',
          },
        },
      },
      registry_selection: {
        meta: {
          title: 'Select Schemas',
          description: 'Select which schemas from each AWS Glue registry to migrate. All versions will be migrated 1:1.',
          schema,
          uiSchema,
        },
        on: {
          NEXT: {
            target: 'confirmation',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'confluent_cloud_schema_registry_url',
            actions: 'undo_save_step_data',
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
            target: 'registry_selection',
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
