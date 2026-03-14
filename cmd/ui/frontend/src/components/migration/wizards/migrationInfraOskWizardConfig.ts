import type { WizardConfig } from './types'
import { getOSKClusterDataById } from '@/stores/store'
import { targetClusterProperties, targetClusterUiSchema, jumpClusterTargetProperties, jumpClusterTargetUiSchema } from './sharedWizardSchemas'

export const createMigrationInfraOskWizardConfig = (clusterKey: string): WizardConfig => {
  // clusterKey format is "osk:<clusterId>", strip prefix
  const clusterId = clusterKey.startsWith('osk:') ? clusterKey.slice(4) : clusterKey
  const cluster = getOSKClusterDataById(clusterId)

  const bootstrapServers = cluster?.bootstrap_servers?.join(',') || ''
  const kafkaClusterId = (cluster?.kafka_admin_client_information as any)?.cluster_id || ''

  return {
    id: 'migration-infra-osk-wizard',
    title: 'OSK Migration Infrastructure Wizard',
    description: 'Configure your migration infrastructure for OSK to Confluent Cloud migration',
    apiEndpoint: '/assets/migration',
    initial: 'confluent_cloud_endpoints_question',

    states: {
      // Step 1: Public or Private?
      confluent_cloud_endpoints_question: {
        meta: {
          title: 'Kafka Migration - Public or Private Networking',
          description: 'When migrating from Kafka to Confluent Cloud, you can choose to use public or private networking. Public networking is the default and requires no additional configuration. Private networking is more complex involving private linking and jump clusters.',
          schema: {
            type: 'object',
            properties: {
              has_public_brokers: {
                type: 'boolean',
                title: 'Are your Kafka brokers publicly accessible?',
                oneOf: [
                  { title: 'Yes', const: true },
                  { title: 'No', const: false },
                ],
              },
            },
            required: ['has_public_brokers'],
          },
          uiSchema: {
            has_public_brokers: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'public_cluster_link_inputs',
              guard: 'has_public_brokers',
              actions: 'save_step_data',
            },
            {
              target: 'private_migration_method_question',
              guard: 'has_private_cc_endpoints',
              actions: 'save_step_data',
            },
          ],
        },
      },

      // Public path: cluster link inputs
      public_cluster_link_inputs: {
        meta: {
          title: 'Public Migration | Cluster Link Configuration',
          description: 'Enter configuration details for your Kafka to Confluent Cloud cluster link',
          schema: {
            type: 'object',
            properties: {
              ...targetClusterProperties(),
              source_cluster_id: {
                type: 'string',
                title: 'Source Kafka Cluster ID',
                default: kafkaClusterId || undefined,
              },
              source_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'Source Kafka Bootstrap Servers',
                default: bootstrapServers || undefined,
              },
            },
            required: ['target_cluster_id', 'target_rest_endpoint', 'cluster_link_name', 'source_cluster_id', 'source_sasl_scram_bootstrap_servers'],
          },
          uiSchema: {
            ...targetClusterUiSchema(),
            cluster_link_name: {
              'ui:placeholder': 'e.g., osk-to-cc-migration-link',
            },
            source_cluster_id: {
              'ui:disabled': !!kafkaClusterId,
            },
            source_sasl_scram_bootstrap_servers: {
              'ui:disabled': !!bootstrapServers,
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

      // Private path: method question
      private_migration_method_question: {
        meta: {
          title: 'Private Migration | Method',
          description: 'Kafka to Confluent Cloud migrations can be performed through either jump clusters or external outbound cluster linking.',
          schema: {
            type: 'object',
            properties: {
              use_jump_clusters: {
                type: 'boolean',
                title: 'Do you want to use jump clusters for your migration?',
                oneOf: [
                  { title: 'Yes', const: true },
                  { title: 'No, use external outbound cluster linking', const: false },
                ],
              },
            },
            required: ['use_jump_clusters'],
          },
          uiSchema: {
            use_jump_clusters: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'external_outbound_cluster_linking_inputs',
              guard: 'use_external_outbound_cluster_linking',
              actions: 'save_step_data',
            },
            {
              target: 'private_link_internet_gateway_question',
              guard: 'use_jump_clusters',
              actions: 'save_step_data',
            },
          ],
          BACK: {
            target: 'confluent_cloud_endpoints_question',
            actions: 'undo_save_step_data',
          },
        },
      },

      // External outbound path - all fields editable for OSK (no AWS defaults)
      external_outbound_cluster_linking_inputs: {
        meta: {
          title: 'Private Migration | External Outbound Cluster Linking',
          description: 'Enter configuration details for your external outbound cluster linking',
          schema: {
            type: 'object',
            properties: {
              ...targetClusterProperties(),
              ...jumpClusterTargetProperties(),
              ext_outbound_subnet_id: {
                type: 'string',
                title: 'Subnet ID',
              },
              ext_outbound_security_group_id: {
                type: 'string',
                title: 'Security Group ID',
              },
              source_region: {
                type: 'string',
                title: 'AWS Region',
              },
              source_cluster_id: {
                type: 'string',
                title: 'Source Kafka Cluster ID',
                default: kafkaClusterId || undefined,
              },
              source_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'Source Kafka Bootstrap Servers',
                default: bootstrapServers || undefined,
              },
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
              },
              aws_kafka_brokers: {
                type: 'array',
                title: 'Kafka Brokers',
                items: {
                  type: 'object',
                  properties: {
                    broker_id: {
                      type: 'string',
                      title: 'Broker ID',
                    },
                    subnet_id: {
                      type: 'string',
                      title: 'Subnet ID',
                    },
                    endpoints: {
                      type: 'array',
                      title: 'Endpoints',
                      items: {
                        type: 'object',
                        properties: {
                          host: {
                            type: 'string',
                            title: 'Host',
                          },
                          port: {
                            type: 'number',
                            title: 'Port',
                          },
                          ip: {
                            type: 'string',
                            title: 'IP',
                          },
                        },
                      },
                    },
                  },
                },
              },
            },
            required: ['cluster_link_name', 'target_environment_id', 'target_cluster_id', 'target_rest_endpoint', 'ext_outbound_subnet_id', 'ext_outbound_security_group_id', 'source_region', 'vpc_id', 'source_cluster_id', 'source_sasl_scram_bootstrap_servers', 'aws_kafka_brokers'],
          },
          uiSchema: {
            ...targetClusterUiSchema(),
            cluster_link_name: {
              'ui:placeholder': 'e.g., osk-to-cc-migration-link',
            },
            ...jumpClusterTargetUiSchema(),
            ext_outbound_subnet_id: {
              'ui:placeholder': 'e.g., subnet-xxxxxxxx',
            },
            ext_outbound_security_group_id: {
              'ui:placeholder': 'e.g., sg-xxxxxxxx',
            },
            source_region: {
              'ui:placeholder': 'e.g., us-east-1',
            },
            source_cluster_id: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_sasl_scram_bootstrap_servers: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            vpc_id: {
              'ui:placeholder': 'e.g., vpc-xxxxxxxx',
            },
            aws_kafka_brokers: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
              'ui:options': {
                addable: true,
                orderable: false,
                removable: true,
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
            target: 'private_migration_method_question',
            actions: 'undo_save_step_data',
          },
        },
      },

      // Jump cluster path: internet gateway question
      private_link_internet_gateway_question: {
        meta: {
          title: 'Private Migration | Private Link - Internet Gateway',
          description: 'When migrating data from Kafka to Confluent Cloud over a private network, a jump cluster is required and some dependencies will need to be installed on the jump cluster brokers from the internet.',
          schema: {
            type: 'object',
            properties: {
              has_existing_internet_gateway: {
                type: 'boolean',
                title: 'Does your VPC network have an existing internet gateway?',
                oneOf: [
                  { title: 'Yes', const: true },
                  { title: 'No', const: false },
                ],
              },
            },
            required: ['has_existing_internet_gateway'],
          },
          uiSchema: {
            has_existing_internet_gateway: {
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
            target: 'private_migration_method_question',
            actions: 'undo_save_step_data',
          },
        },
      },

      // Jump cluster: networking inputs - all editable for OSK (no AWS defaults)
      jump_cluster_networking_inputs: {
        meta: {
          title: 'Private Migration | Jump Cluster - Configuration',
          description: 'Enter configuration details for your jump cluster networking. All fields are required for OSK sources.',
          schema: {
            type: 'object',
            properties: {
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
              },
              ...jumpClusterTargetProperties(),
              jump_cluster_instance_type: {
                type: 'string',
                title: 'Instance Type',
              },
              jump_cluster_broker_storage: {
                type: 'number',
                title: 'Storage per Broker (GB)',
              },
              jump_cluster_broker_subnet_cidr: {
                type: 'array',
                title: 'Broker Subnet CIDR Range',
                description: 'The number of subnets to create determines the number of jump cluster brokers that will be created.',
                items: {
                  type: 'string',
                },
                default: ['', '', ''],
              },
              jump_cluster_setup_host_subnet_cidr: {
                type: 'string',
                title: 'Jump Cluster Setup Host CIDR',
                description: 'The subnet CIDR range for the EC2 instance that will provision the jump cluster instances.',
              },
            },
            required: ['vpc_id', 'existing_private_link_vpce_id', 'jump_cluster_instance_type', 'jump_cluster_broker_storage', 'jump_cluster_broker_subnet_cidr', 'jump_cluster_setup_host_subnet_cidr'],
          },
          uiSchema: {
            vpc_id: {
              'ui:placeholder': 'e.g., vpc-xxxxxxxx',
            },
            ...jumpClusterTargetUiSchema(),
            jump_cluster_instance_type: {
              'ui:placeholder': 'e.g., m5.xlarge',
            },
            jump_cluster_broker_storage: {
              'ui:placeholder': 'e.g., 500',
            },
            jump_cluster_broker_subnet_cidr: {
              'ui:placeholder': 'e.g., 10.0.1.0/24,10.0.2.0/24,10.0.3.0/24',
              'ui:options': {
                addable: true,
                orderable: false,
                removable: true,
              },
            },
            jump_cluster_setup_host_subnet_cidr: {
              'ui:placeholder': 'e.g., 10.0.4.0/24',
            },
          },
        },
        on: {
          // OSK goes directly to SASL/SCRAM auth (no IAM path)
          NEXT: {
            target: 'jump_cluster_authentication_sasl_scram',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'private_link_internet_gateway_question',
            actions: 'undo_save_step_data',
          },
        },
      },

      // Jump cluster: SASL/SCRAM auth (direct - no auth question for OSK)
      jump_cluster_authentication_sasl_scram: {
        meta: {
          title: 'Private Migration | Jump Cluster - Authentication (SASL/SCRAM)',
          description: 'Configure the cluster link between the jump cluster and Confluent Cloud.',
          schema: {
            type: 'object',
            properties: {
              jump_cluster_auth_type: {
                type: 'string',
                title: 'Authentication Type',
                default: 'sasl_scram',
              },
              source_cluster_id: {
                type: 'string',
                title: 'Source Kafka Cluster ID',
                default: kafkaClusterId || undefined,
              },
              source_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'Source Kafka Bootstrap Servers',
                default: bootstrapServers || undefined,
              },
              source_region: {
                type: 'string',
                title: 'AWS Region',
              },
              ...targetClusterProperties(),
              ...jumpClusterTargetProperties(),
            },
            required: ['source_cluster_id', 'source_sasl_scram_bootstrap_servers', 'source_region', 'target_environment_id', 'target_cluster_id', 'target_rest_endpoint', 'target_bootstrap_endpoint', 'cluster_link_name'],
          },
          uiSchema: {
            jump_cluster_auth_type: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_cluster_id: {
              'ui:disabled': !!kafkaClusterId,
            },
            source_sasl_scram_bootstrap_servers: {
              'ui:disabled': !!bootstrapServers,
            },
            source_region: {
              'ui:placeholder': 'e.g., us-east-1',
            },
            ...targetClusterUiSchema(),
            cluster_link_name: {
              'ui:placeholder': 'e.g., osk-to-cc-migration-link',
            },
            ...jumpClusterTargetUiSchema(),
          },
        },
        on: {
          NEXT: {
            target: 'confirmation',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'jump_cluster_networking_inputs',
            actions: 'undo_save_step_data',
          },
        },
      },

      // Confirmation step
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
              target: 'external_outbound_cluster_linking_inputs',
              guard: 'came_from_external_outbound_cluster_linking_inputs',
              actions: 'undo_save_step_data',
            },
            {
              target: 'jump_cluster_authentication_sasl_scram',
              guard: 'came_from_jump_cluster_authentication_sasl_scram',
              actions: 'undo_save_step_data',
            },
          ],
        },
      },

      // Complete state
      complete: {
        type: 'final',
        meta: {
          title: 'Configuration Complete',
          message: 'Your migration infrastructure configuration is ready to be processed...',
        },
      },
    },

    guards: {
      has_public_brokers: ({ event }) => {
        return event.data?.has_public_brokers === true
      },
      has_private_cc_endpoints: ({ event }) => {
        return event.data?.has_public_brokers === false
      },
      use_jump_clusters: ({ event }) => {
        return event.data?.use_jump_clusters === true
      },
      use_external_outbound_cluster_linking: ({ event }) => {
        return event.data?.use_jump_clusters === false
      },
      came_from_public_cluster_link_inputs: ({ context }) => {
        return context.previousStep === 'public_cluster_link_inputs'
      },
      came_from_external_outbound_cluster_linking_inputs: ({ context }) => {
        return context.previousStep === 'external_outbound_cluster_linking_inputs'
      },
      came_from_jump_cluster_authentication_sasl_scram: ({ context }) => {
        return context.previousStep === 'jump_cluster_authentication_sasl_scram'
      },
    },

    actions: {
      save_step_data: 'save_step_data',
      undo_save_step_data: 'undo_save_step_data',
    },
  }
}
