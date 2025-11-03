import type { WizardConfig } from './types'

export const migrationInfraWizardConfig: WizardConfig = {
  id: 'migration-infra-wizard',
  title: 'Migration Infrastructure Wizard',
  description: 'Configure your migration infrastructure for migration',
  apiEndpoint: '/assets/migration',
  initial: 'confluent_cloud_endpoints_question',

  states: {
    confluent_cloud_endpoints_question: {
      meta: {
        title: 'Are your Confluent Cloud endpoints publicly accessible or private networked?',
        description: 'PLACEHOLDER',
        schema: {
          type: 'object',
          properties: {
            has_public_cc_endpoints: {
              type: 'boolean',
              title: 'Are your Confluent Cloud endpoints publicly accessible?',
              oneOf: [
                { title: 'Yes', const: true },
                { title: 'No', const: false },
              ],
            },
          },
          required: ['has_public_cc_endpoints'],
        },
        uiSchema: {
          has_public_cc_endpoints: {
            'ui:widget': 'radio',
          },
        },
      },
      on: {
        NEXT: [
          {
            target: 'public_cluster_link_inputs',
            guard: 'has_public_cc_endpoints',
            actions: 'save_step_data',
          },
          {
            target: 'private_link_subnets_question',
            guard: 'has_private_cc_endpoints',
            actions: 'save_step_data',
          },
        ],
      },
    },
    public_cluster_link_inputs: {
      meta: {
        title: 'Public-Public Cluster Link Configuration',
        description: 'Enter configuration details for your MSK to Confluent Cloud public-public cluster link',
        schema: {
          type: 'object',
          properties: {
            target_cluster_id: {
              type: 'string',
              title: 'Confluent Cloud Cluster ID'
            },
            target_rest_endpoint: {
              type: 'string',
              title: 'Confluent Cloud Cluster REST Endpoint'
            },
            cluster_link_name: {
              type: 'string',
              title: 'Confluent Cloud Cluster Link Name'
            },
            msk_cluster_id: {
              type: 'string',
              title: 'MSK Cluster ID (retrieved from statefile)'
            },
            msk_sasl_scram_bootstrap_servers: {
              type: 'string',
              title: 'MSK Bootstrap Servers (retrieved from statefile)'
            },
          },
          required: ['target_cluster_id', 'target_rest_endpoint', 'cluster_link_name', 'msk_cluster_id', 'msk_sasl_scram_bootstrap_servers'],
        },
        uiSchema: {
          target_cluster_id: {
            'ui:placeholder': 'e.g., lkc-xxxxxx',
          },
          target_rest_endpoint: {
            'ui:placeholder': 'e.g., https://xxx.xxx.aws.confluent.cloud:443',
          },
          cluster_link_name: {
            'ui:placeholder': 'e.g., msk-to-cc-migration-link',
          },
          msk_cluster_id: {
            'ui:placeholder': 'e.g., 3Db5QLSqSZieL3rJBUUegA',
          },
          msk_sasl_scram_bootstrap_servers: {
            'ui:placeholder': 'e.g., b-1.examplecluster.0abcde.c.us-west-2.msk.amazonaws.com:9198,b-2.examplecluster.0abcde.c.us-west-2.msk.amazonaws.com:9198',
          },
        },
      },
      on: {
        NEXT: {
          target: 'confirmation',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'confluent_cloud_endpoints_question',
          actions: 'undo_save_step_data',
        },
      },
    },
    private_link_subnets_question: {
      meta: {
        title: 'Reuse existing subnets or create new subnets for setting up a private link to Confluent Cloud',
        description: 'PLACEHOLDER',
        schema: {
          type: 'object',
          properties: {
            reuse_existing_subnets: {
              type: 'boolean',
              title: 'Do you want to reuse existing subnets?',
              oneOf: [
                { title: 'Yes', const: true },
                { title: 'No', const: false },
              ],
            },
          },
          required: ['reuse_existing_subnets'],
        },
        uiSchema: {
          reuse_existing_subnets: {
            'ui:widget': 'radio',
          },
        },
      },
      on: {
        NEXT: [
          {
            target: 'private_link_reuse_existing_subnets',
            guard: 'reuse_existing_subnets',
            actions: 'save_step_data',
          },
          {
            target: 'private_link_create_new_subnets',
            guard: 'create_new_subnets',
            actions: 'save_step_data',
          },
        ],
        BACK: {
          target: 'confluent_cloud_endpoints_question',
          actions: 'undo_save_step_data',
        },
      },
    },
    private_link_reuse_existing_subnets: {
      meta: {
        title: 'Reuse existing subnets',
        description: 'PLACEHOLDER',
        schema: {
          type: 'object',
          properties: {
            vpc_id: {
              type: 'string',
              title: 'VPC ID (retrieved from statefile)',
            },
            existing_subnets: {
              type: 'array',
              title: 'Existing subnet IDs',
              items: {
                type: 'string',
              },
            },
          },
        },
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
              title: 'What type of target Confluent Cloud cluster are you migrating to?',
              enum: ['dedicated'],
              enumNames: ['Dedicated'],
            },
          },
          required: ['target_cluster_type'],
        },
        uiSchema: {
          target_cluster_type: {
            'ui:widget': 'radio',
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
          target: 'public_cluster_link_inputs',
          actions: 'undo_save_step_data',
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
          actions: 'undo_save_step_data',
        },
      },
    },
    statefile_inputs: {
      meta: {
        title: 'Statefile Configuration',
        description:
          'Enter configuration details for your statefile - WIP these will be parsed from the statefile in future and this stage removed',
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
          required: [
            'msk_cluster_id',
            'msk_sasl_scram_bootstrap_servers',
            'msk_publicly_accessible',
          ],
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
            'ui:widget': 'radio',
          },
        },
      },
      on: {
        NEXT: {
          target: 'confirmation',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'dedicated_inputs',
          actions: 'undo_save_step_data',
        },
      },
    },
    confirmation: {
      meta: {
        title: 'Review Configuration',
        description: 'Review your configuration before generating Terraform files',
      },
      on: {
        CONFIRM: {
          target: 'complete',
        },
        BACK: {
          target: 'statefile_inputs',
          actions: 'undo_save_step_data',
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
    has_public_cc_endpoints: ({ event}) => {
      return event.data?.has_public_cc_endpoints === true
    },
    has_private_cc_endpoints: ({ event}) => {
      return event.data?.has_public_cc_endpoints === false
    },
    reuse_existing_subnets: ({ event }) => {
      return event.data?.reuse_existing_subnets === true
    },
    create_new_subnets: ({ event }) => {
      return event.data?.reuse_existing_subnets === false
    },




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
    undo_save_step_data: 'undo_save_step_data',
  },
}
