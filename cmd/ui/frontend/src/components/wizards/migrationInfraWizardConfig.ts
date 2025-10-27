import type { WizardConfig } from './types'

export const migrationInfraWizardConfig: WizardConfig = {
  id: 'migration-infra-wizard',
  title: 'Migration Infrastructure Wizard',
  description: 'Configure your migration infrastructure for migration',
  apiEndpoint: '/assets/migration',
  initial: 'authentication_method_question',

  states: {
    authentication_method_question: {
      meta: {
        title: 'Authentication Method',
        description: 'Which authentication method will you use for the cluster link?',
        schema: {
          type: 'object',
          properties: {
            authentication_method: {
              type: 'string',
              title: 'Which authentication method will you use for the cluster link?',
              enum: ['sasl_scram'],
              enumNames: ['SASL/SCRAM'],
            },
          },
          required: ['authentication_method'],
        },
        uiSchema: {
          authentication_method: {
            'ui:widget': 'select',
          },
        },
      },
      on: {
        NEXT: [
          {
            target: 'cluster_type_question',
            guard: 'is_sasl_scram',
            actions: 'save_step_data',
          },
        ],
      },
    },
    cluster_type_question: {
      meta: {
        title: 'Target Cluster Type',
        description: 'Select the type of your target Confluent Cloud cluster',
        schema: {
          type: 'object',
          properties: {
            target_cluster_type: {
              type: 'string',
              title: "What type of target Confluent Cloud cluster are you migrating to?",
              enum: ['dedicated'],
              enumNames: ['Dedicated'],
            },
          },
          required: ['target_cluster_type'],
        },
        uiSchema: {
          target_cluster_type: {
            'ui:widget': 'select',
          },
        },
      },
      on: {
        NEXT: [
          {
            target: 'dedicated_inputs',
            guard: 'is_dedicated',
            actions: 'save_step_data',
          },
        ],
        BACK: {
          target: 'authentication_method_question',
        },
      },
    },
    dedicated_inputs: {
      meta: {
        title: 'Dedicated Cluster Configuration',
        description: 'Enter configuration details for your Dedicated cluster',
        schema: {
          type: 'object',
          properties: {
            target_environment_id: {
              type: 'string',
              title: 'Confluent Cloud Environment ID',
            },
            target_cluster_id: {
              type: 'string',
              title: 'Confluent Cloud Cluster ID',
            },
            target_rest_endpoint: {
              type: 'string',
              title: 'Confluent Cloud Cluster REST Endpoint',
            },
          },
          required: ['target_environment_id', 'target_cluster_id', 'target_rest_endpoint'],
        },
        uiSchema: {
          target_environment_id: {
            'ui:placeholder': 'e.g., env-xxxxx',
          },
          target_cluster_id: {
            'ui:placeholder': 'e.g., cluster-xxxxx',
          },
          target_rest_endpoint: {
            'ui:placeholder': 'e.g., https://api.confluent.cloud',
          },
        },
      },
      on: {
        NEXT: {
          target: 'statefile_inputs',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'cluster_type_question',
        },
      },
    },
    statefile_inputs: {
      meta: {
        title: 'Statefile Configuration',
        description: 'Enter configuration details for your statefile - WIP these will be parsed from the statefile in future and this stage removed',
        schema: {
          type: 'object',
          properties: {
            msk_cluster_id: {
              type: 'string',
              title: 'MSK Cluster ID',
            },
            msk_sasl_scram_bootstrap_servers: {
              type: 'string',
              title: 'MSK Cluster Bootstrap Brokers',
            },
            msk_publicly_accessible: {
              type: 'boolean',
              title: 'Is your MSK cluster accessible from the internet?',
              oneOf: [
                { title: 'Yes', const: true },
                { title: 'No', const: false },
              ],
              default: false,
            },
          },
          required: ['msk_cluster_id', 'msk_sasl_scram_bootstrap_servers', 'msk_publicly_accessible'],
        },
        uiSchema: {
          msk_cluster_id: {
            'ui:placeholder': 'e.g., cluster-xxxxx',
          },
          msk_sasl_scram_bootstrap_servers: {
            'ui:placeholder':
              'e.g., b-1.examplecluster.0abcde.c.us-west-2.msk.amazonaws.com:9098,b-2.examplecluster.0abcde.c.us-west-2.msk.amazonaws.com:9098',
          },
          msk_publicly_accessible: {
            "ui:widget": "radio",
          }
        },
      },
      on: {
        NEXT: {
          target: 'complete',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'dedicated_inputs',
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

  guards: {
    is_dedicated: ({ event }) => {
      return event.data?.target_cluster_type === 'dedicated'
    },
    is_sasl_scram: ({ event }) => {
      return event.data?.authentication_method === 'sasl_scram'
    },
    came_from_dedicated_inputs: ({ context }) => {
      return context.previousStep === 'dedicated_inputs'
    },
    came_from_statefile_inputs: ({ context }) => {
      return context.previousStep === 'statefile_inputs'
    },
  },

  actions: {
    save_step_data: 'save_step_data',
  },
}
