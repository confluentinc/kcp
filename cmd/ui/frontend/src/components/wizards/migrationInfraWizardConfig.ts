import type { WizardConfig } from './types'

export const migrationInfraWizardConfig: WizardConfig = {
  id: 'migration-infra-wizard',
  title: 'Migration Infrastructure Wizard',
  description: 'Configure infrastructure for your migration',
  apiEndpoint: '/migration-infra',

  states: {
    migration_type: {
      meta: {
        title: 'Migration Type',
        description: 'What type of migration are you planning?',
        schema: {
          type: 'object',
          properties: {
            migration_type: {
              type: 'string',
              enum: ['lift-and-shift', 'replatform', 'refactor', 'rearchitect'],
              title: 'Migration Type',
              description: 'Select the type of migration strategy',
            },
          },
          required: ['migration_type'],
        },
        uiSchema: {
          migration_type: {
            'ui:widget': 'select',
          },
        },
      },
      on: {
        NEXT: {
          target: 'source_environment',
          actions: 'save_step_data',
        },
      },
    },
    source_environment: {
      meta: {
        title: 'Source Environment',
        description: 'Configure your source environment details',
        schema: {
          type: 'object',
          properties: {
            source_platform: {
              type: 'string',
              enum: ['on-premises', 'aws', 'azure', 'gcp', 'vmware'],
              title: 'Source Platform',
              description: 'Where is your current infrastructure running?',
            },
            source_region: {
              type: 'string',
              title: 'Source Region',
              description: 'Primary region of your source environment',
            },
            workload_count: {
              type: 'integer',
              title: 'Number of Workloads',
              description: 'How many workloads need to be migrated?',
              minimum: 1,
              maximum: 10000,
            },
          },
          required: ['source_platform', 'source_region', 'workload_count'],
        },
        uiSchema: {
          source_platform: {
            'ui:widget': 'select',
          },
          workload_count: {
            'ui:widget': 'updown',
          },
        },
      },
      on: {
        NEXT: {
          target: 'target_environment',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'migration_type',
        },
      },
    },
    target_environment: {
      meta: {
        title: 'Target Environment',
        description: 'Configure your target environment details',
        schema: {
          type: 'object',
          properties: {
            target_platform: {
              type: 'string',
              enum: ['aws', 'azure', 'gcp', 'hybrid-cloud'],
              title: 'Target Platform',
              description: 'Where will you migrate to?',
            },
            target_region: {
              type: 'string',
              title: 'Target Region',
              description: 'Primary region for your target environment',
            },
            migration_timeline: {
              type: 'string',
              enum: ['immediate', '3-months', '6-months', '12-months', 'custom'],
              title: 'Migration Timeline',
              description: 'When do you plan to complete the migration?',
            },
          },
          required: ['target_platform', 'target_region', 'migration_timeline'],
        },
        uiSchema: {
          target_platform: {
            'ui:widget': 'select',
          },
          migration_timeline: {
            'ui:widget': 'select',
          },
        },
      },
      on: {
        NEXT: {
          target: 'migration_tools',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'source_environment',
        },
      },
    },
    migration_tools: {
      meta: {
        title: 'Migration Tools',
        description: 'Select tools and services for your migration',
        schema: {
          type: 'object',
          properties: {
            migration_tool: {
              type: 'string',
              enum: ['aws-mgn', 'azure-migrate', 'vmware-hcx', 'custom'],
              title: 'Migration Tool',
              description: 'Which migration tool will you use?',
            },
            backup_strategy: {
              type: 'boolean',
              title: 'Enable Backup Strategy',
              description: 'Implement backup and disaster recovery',
              default: true,
            },
            monitoring_enabled: {
              type: 'boolean',
              title: 'Enable Monitoring',
              description: 'Set up monitoring and alerting',
              default: true,
            },
            security_compliance: {
              type: 'string',
              enum: ['basic', 'enhanced', 'enterprise'],
              title: 'Security Compliance Level',
              description: 'Required security and compliance level',
            },
          },
          required: ['migration_tool', 'security_compliance'],
        },
        uiSchema: {
          migration_tool: {
            'ui:widget': 'select',
          },
          security_compliance: {
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
          target: 'target_environment',
        },
      },
    },
    complete: {
      type: 'final',
      meta: {
        title: 'Configuration Complete',
        message: 'Your migration infrastructure configuration is ready to be processed...',
      },
    },
  },

  guards: {},

  actions: {
    save_step_data: 'save_step_data',
  },
}
