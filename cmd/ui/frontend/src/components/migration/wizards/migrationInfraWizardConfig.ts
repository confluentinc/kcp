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
        title: 'MSK Migration - Public or Private Networking',
        description: 'When migrating from MSK to Confluent Cloud, you can choose to use public or private networking. Public networking is the default and requires no additional configuration. Private networking is more complex involving private linking and jump clusters.',
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
        title: 'Public Migration | Cluster Link Configuration',
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
        title: 'Private Migration |Private Link - Subnets',
        description: 'When setting up a private link between Confluent Cloud and AWS, subnets need to be specified to establish the connection.',
        schema: {
          type: 'object',
          properties: {
            reuse_existing_subnets: {
              type: 'boolean',
              title: 'Do you want to reuse existing subnets for setting up a private link to Confluent Cloud?',
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
        title: 'Private Migration | Private Link - Reuse Existing Subnets',
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
              minItems: 3, // retrieve number of subnets from statefile
              maxItems: 3,
              default: ['', '', ''],
            },
          },
          required: ['vpc_id', 'existing_subnets'],
        },
        uiSchema: {
          vpc_id: {
            'ui:placeholder': 'e.g., vpc-xxxx',
            'ui:readonly': true,
          },
          existing_subnets: {
            'ui:placeholder': 'e.g., subnet-xxxx,subnet-xxxx,subnet-xxxx',
            'ui:options': {
              addable: false,
              orderable: false,
              removable: false,
            },
          },
        },
      },
      on: {
        NEXT: {
          target: 'confirmation',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'private_link_subnets_question',
          actions: 'undo_save_step_data',
        },
      },
    },
    private_link_create_new_subnets: {
      meta: {
        title: 'Private Migration | Private Link - New Subnets',
        description: 'PLACEHOLDER',
        schema: {
          type: 'object',
          properties: {
            vpc_id: {
              type: 'string',
              title: 'VPC ID (retrieved from statefile)',
            },
            new_subnets: {
              type: 'array',
              title: 'New subnet CIDR ranges',
              items: {
                type: 'string',
              },
              minItems: 3,
              maxItems: 3,
              default: ['', '', ''],
            },
          }
        },
        uiSchema: {
          vpc_id: {
            'ui:placeholder': 'e.g., vpc-xxxx',
          },
          new_subnets: {
            items: {
              'ui:placeholder': 'e.g., 10.0.1.0/24',
            },
            'ui:options': {
              addable: false,
              orderable: false,
              removable: false,
            },
          },
        },
      },
      on: {
        NEXT: {
          target: 'private_link_internet_gateway_question',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'private_link_subnets_question',
          actions: 'undo_save_step_data',
        },
      },
    },
    private_link_internet_gateway_question: {
      meta: {
        title: 'Private Migration | Private Link - Internet Gateway',
        description: 'When migrating data from MSK to Confluent Cloud over a private network, a jump cluster is required and some dependencies will need to be installed on these jump clusters from the internet.',
        schema: {
          type: 'object',
          properties: {
            reuse_existing_internet_gateway: {
              type: 'boolean',
              title: 'Does your MSK VPC network have an existing internet gateway?',
              oneOf: [
                { title: 'Yes', const: true },
                { title: 'No', const: false },
              ],
            },
          },
          required: ['reuse_existing_internet_gateway'],
        },
        uiSchema: {
          reuse_existing_internet_gateway: {
            'ui:widget': 'radio',
          },
        },
      },
      on: {
        NEXT: {
          target: 'jump_cluster_networking_inputs',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'private_link_create_new_subnets',
          actions: 'undo_save_step_data',
        },
      },
    },
    jump_cluster_networking_inputs: {
      meta: {
        title: 'Private Migration | Jump Cluster - Configuration',
        description: 'Enter configuration details for your jump cluster networking',
        schema: {
          type: 'object',
          properties: {
            vpc_id: {
              type: 'string',
              title: 'VPC ID (retrieved from statefile)',
            },
            jump_cluster_instance_type: {
              type: 'string',
              title: 'Instance Type', // retrieved and formatted from statefile - kafka.m5.large == m5.large
            },
            jump_cluster_broker_total: {
              type: 'number',
              title: 'Broker Total', // retrieved from statefile based on MSK broker node total.
            },
            jump_cluster_broker_storage: {
              type: 'number',
              title: 'Broker Storage per Broker (GB)', // retrieved from statefile based on MSK broker node storage.
            },
            jump_cluster_broker_subnet_cidr: {
              type: 'array',
              title: 'Broker Subnet CIDR Range',
              items: {
                type: 'string',
              },
              minItems: 3, // retrievd from number of broker nodes in MSK from statefile.
              maxItems: 3, // retrievd from number of broker nodes in MSK from statefile.
              default: ['', '', ''],
            },
            ansible_instance_subnet_cidr: {
              type: 'string',
              title: 'Ansible Instance Subnet CIDR', // Better name for user with no context.
            }
          },
          required: ['vpc_id', 'jump_cluster_instance_type', 'jump_cluster_broker_total', 'jump_cluster_broker_storage', 'jump_cluster_broker_subnet_cidr', 'ansible_instance_subnet_cidr'],
          },
        uiSchema: {
          vpc_id: {
            'ui:placeholder': 'e.g., vpc-xxxx',
            'ui:readonly': true,
          },
          jump_cluster_instance_type: {
            'ui:placeholder': 'e.g., m5.large',
          },
          jump_cluster_broker_total: {
            'ui:placeholder': 'e.g., 3',
          },
          jump_cluster_broker_storage: {
            'ui:placeholder': 'e.g., 100',
          },
          jump_cluster_broker_subnet_cidr: {
            'ui:placeholder': 'e.g., 10.0.1.0/24,10.0.2.0/24,10.0.3.0/24',
            'ui:options': {
              addable: true,
              orderable: false,
              removable: true,
            },
          },
          ansible_instance_subnet_cidr: {
            'ui:placeholder': 'e.g., 10.0.4.0/24',
          },
        },
      },
      on: {
        NEXT: {
          target: 'msk_jump_cluster_authentication_question',
          actions: 'save_step_data',
        },
        BACK: {
          target: 'private_link_internet_gateway_question',
          actions: 'undo_save_step_data',
        },
      },
    },
    msk_jump_cluster_authentication_question: {
      meta: {
        title: 'Private Migration | Jump Cluster - Authentication',
        description: 'How will the jump cluster authenticate to the MSK cluster?',
        schema: {
          type: 'object',
          properties: {
            msk_jump_cluster_auth_type: {
              type: 'string',
              title: 'MSK Jump Cluster Authentication Type',
              enum: ['sasl_scram', 'iam'],
              enumNames: ['SASL/SCRAM', 'IAM'],
            },
          },
          required: ['msk_jump_cluster_auth_type'],
        },
        uiSchema: {
          msk_jump_cluster_auth_type: {
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
          target: 'msk_jump_cluster_authentication_question',
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
        BACK: [
          {
            target: 'public_cluster_link_inputs',
            guard: 'came_from_public_cluster_link_inputs',
            actions: 'undo_save_step_data',
          },
          {
            target: 'msk_jump_cluster_authentication_question',
            guard: 'came_from_private_cluster_link_inputs',
            actions: 'undo_save_step_data',
          }
        ]
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
    came_from_public_cluster_link_inputs: ({ context }) => {
      return context.previousStep === 'public_cluster_link_inputs'
    },
    came_from_msk_jump_cluster_authentication_question: ({ context }) => {
      return context.previousStep === 'msk_jump_cluster_authentication_question'
    }
  },

  actions: {
    save_step_data: 'save_step_data',
    undo_save_step_data: 'undo_save_step_data',
  },
}
