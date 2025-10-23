import type { WizardConfig } from './types'

export const migrationScriptsWizardConfig: WizardConfig = {
  id: 'migration-scripts-wizard',
  title: 'Migration Scripts Wizard',
  description: 'Generate migration scripts and automation tools',
  apiEndpoint: '/migration-scripts',

  states: {
    script_type: {
      meta: {
        title: 'Script Type',
        description: 'What type of migration scripts do you need?',
        schema: {
          type: 'object',
          properties: {
            script_type: {
              type: 'string',
              enum: [
                'data-migration',
                'application-migration',
                'infrastructure-migration',
                'database-migration',
              ],
              title: 'Script Type',
              description: 'Select the type of migration scripts needed',
            },
          },
          required: ['script_type'],
        },
        uiSchema: {
          script_type: {
            'ui:widget': 'select',
          },
        },
      },
      on: {
        NEXT: {
          target: 'source_system',
          actions: 'save_step_data',
        },
      },
    },
    source_system: {
      meta: {
        title: 'Source System',
        description: 'Configure your source system details',
        schema: {
          type: 'object',
          properties: {
            source_database: {
              type: 'string',
              enum: ['mysql', 'postgresql', 'oracle', 'sql-server', 'mongodb'],
              title: 'Source Database',
              description: 'What database are you migrating from?',
            },
            source_os: {
              type: 'string',
              enum: ['linux', 'windows', 'unix', 'macos'],
              title: 'Source Operating System',
              description: 'Operating system of the source system',
            },
            data_volume: {
              type: 'string',
              enum: ['small', 'medium', 'large', 'enterprise'],
              title: 'Data Volume',
              description: 'Size of data to be migrated',
            },
          },
          required: ['source_database', 'source_os', 'data_volume'],
        },
        uiSchema: {
          source_database: {
            'ui:widget': 'select',
          },
          source_os: {
            'ui:widget': 'select',
          },
          data_volume: {
            'ui:widget': 'select',
          },
        },
      },
      on: {
        NEXT: {
          target: 'target_system',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'script_type',
        },
      },
    },
    target_system: {
      meta: {
        title: 'Target System',
        description: 'Configure your target system details',
        schema: {
          type: 'object',
          properties: {
            target_database: {
              type: 'string',
              enum: [
                'mysql',
                'postgresql',
                'oracle',
                'sql-server',
                'mongodb',
                'dynamodb',
                'cosmosdb',
              ],
              title: 'Target Database',
              description: 'What database are you migrating to?',
            },
            target_os: {
              type: 'string',
              enum: ['linux', 'windows', 'unix', 'macos'],
              title: 'Target Operating System',
              description: 'Operating system of the target system',
            },
            migration_method: {
              type: 'string',
              enum: ['bulk-load', 'incremental', 'real-time-sync', 'batch-processing'],
              title: 'Migration Method',
              description: 'How should the migration be performed?',
            },
          },
          required: ['target_database', 'target_os', 'migration_method'],
        },
        uiSchema: {
          target_database: {
            'ui:widget': 'select',
          },
          target_os: {
            'ui:widget': 'select',
          },
          migration_method: {
            'ui:widget': 'select',
          },
        },
      },
      on: {
        NEXT: {
          target: 'script_options',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'source_system',
        },
      },
    },
    script_options: {
      meta: {
        title: 'Script Options',
        description: 'Configure script generation options',
        schema: {
          type: 'object',
          properties: {
            script_language: {
              type: 'string',
              enum: ['bash', 'powershell', 'python', 'sql'],
              title: 'Script Language',
              description: 'Preferred scripting language',
            },
            include_validation: {
              type: 'boolean',
              title: 'Include Data Validation',
              description: 'Add data validation checks to scripts',
              default: true,
            },
            include_rollback: {
              type: 'boolean',
              title: 'Include Rollback Scripts',
              description: 'Generate rollback scripts for safety',
              default: true,
            },
            parallel_execution: {
              type: 'boolean',
              title: 'Enable Parallel Execution',
              description: 'Generate scripts for parallel processing',
              default: false,
            },
            logging_level: {
              type: 'string',
              enum: ['basic', 'detailed', 'verbose'],
              title: 'Logging Level',
              description: 'Level of logging in generated scripts',
            },
          },
          required: ['script_language', 'logging_level'],
        },
        uiSchema: {
          script_language: {
            'ui:widget': 'select',
          },
          logging_level: {
            'ui:widget': 'select',
          },
        },
      },
      on: {
        NEXT: {
          target: 'complete',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'target_system',
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
  },
}
