import type { WizardConfig } from './types'
import { getClusterDataByArn } from '@/stores/store'
import {
  targetClusterProperties,
  targetClusterUiSchema,
  jumpClusterTargetProperties,
  jumpClusterTargetUiSchema,
} from './sharedWizardSchemas'

export const createMigrationInfraMskWizardConfig = (clusterArn: string): WizardConfig => {
  const cluster = getClusterDataByArn(clusterArn)

  const instanceType = cluster?.metrics?.metadata?.instance_type || 'kafka.m5.xlarge'

  const brokerNodes =
    cluster?.aws_client_information?.nodes?.filter(
      (node: any) => node.NodeType === 'BROKER' && node.BrokerNodeInfo
    ) || []

  const subnets = cluster?.aws_client_information?.cluster_networking?.subnets || []

  const awsKafkaBrokers = brokerNodes.map((node: any) => {
    const brokerId = node.BrokerNodeInfo.BrokerId
    const matchingSubnet = subnets.find((subnet: any) => subnet.subnet_msk_broker_id === brokerId)

    return {
      broker_id: brokerId?.toString() || '',
      subnet_id: node.BrokerNodeInfo.ClientSubnet || '',
      endpoints:
        node.BrokerNodeInfo.Endpoints?.map((endpoint: string) => ({
          host: endpoint,
          port: 9096,
          ip: matchingSubnet?.private_ip_address || '',
        })) || [],
    }
  })

  return {
    id: 'migration-infra-wizard',
    title: 'Migration Infrastructure Wizard',
    description: 'Configure your migration infrastructure for migration',
    apiEndpoint: '/assets/migration',
    initial: 'confluent_cloud_endpoints_question',

    states: {
      confluent_cloud_endpoints_question: {
        meta: {
          title: 'MSK Migration - Public or Private Networking',
          description:
            'When migrating from MSK to Confluent Cloud, you can choose to use public or private networking. Public networking is the default and requires no additional configuration. Private networking is more complex involving private linking and jump clusters.',
          schema: {
            type: 'object',
            properties: {
              has_public_brokers: {
                type: 'boolean',
                title: 'Are your MSK brokers publicly accessible?',
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
              target: 'target_cluster_type_question',
              guard: 'has_private_cc_endpoints',
              actions: 'save_step_data',
            },
          ],
        },
      },
      public_cluster_link_inputs: {
        meta: {
          title: 'Public Migration | Cluster Link Configuration',
          description:
            'Enter configuration details for your MSK to Confluent Cloud public-public cluster link',
          schema: {
            type: 'object',
            properties: {
              ...targetClusterProperties(),
              source_cluster_id: {
                type: 'string',
                title: 'MSK Cluster ID',
                default:
                  cluster?.kafka_admin_client_information?.cluster_id ||
                  'failed to retrieve MSK cluster ID from statefile.',
              },
              source_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers',
                default:
                  cluster?.aws_client_information?.bootstrap_brokers
                    ?.BootstrapBrokerStringPublicSaslScram ||
                  'failed to retrieve MSK SASL/SCRAM bootstrap servers (public) from statefile.',
              },
              source_sasl_scram_mechanism: {
                type: 'string',
                title: 'Source SASL/SCRAM Mechanism',
                default: cluster?.kafka_admin_client_information?.sasl_mechanism || 'SCRAM-SHA-512',
              },
            },
            required: [
              'target_cluster_id',
              'target_rest_endpoint',
              'cluster_link_name',
              'source_cluster_id',
              'source_sasl_scram_bootstrap_servers',
            ],
          },
          uiSchema: {
            ...targetClusterUiSchema(),
            source_cluster_id: {
              'ui:disabled': true,
            },
            source_sasl_scram_bootstrap_servers: {
              'ui:disabled': true,
            },
            source_sasl_scram_mechanism: {
              'ui:disabled': true,
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
      target_cluster_type_question: {
        meta: {
          title: 'Private Migration | Target Cluster Type',
          description:
            'External outbound cluster linking is only supported for Enterprise clusters.',
          schema: {
            type: 'object',
            properties: {
              target_cluster_type: {
                type: 'string',
                title: 'What is your Confluent Cloud target cluster type?',
                oneOf: [
                  { title: 'Enterprise', const: 'enterprise' },
                  { title: 'Dedicated', const: 'dedicated' },
                ],
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
              target: 'private_migration_method_question',
              guard: 'target_cluster_is_enterprise',
              actions: 'save_step_data',
            },
            {
              target: 'private_link_internet_gateway_question',
              guard: 'target_cluster_is_dedicated',
              actions: 'save_step_data',
            },
          ],
          BACK: {
            target: 'confluent_cloud_endpoints_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      private_migration_method_question: {
        meta: {
          title: 'Private Migration | Method',
          description:
            'MSK to Confluent Cloud migrations can be performed through jump clusters or external outbound cluster linking. External outbound supports SASL/SCRAM or Unauthenticated Plaintext authentication.',
          schema: {
            type: 'object',
            properties: {
              private_migration_method: {
                type: 'string',
                title: 'How do you want to migrate?',
                oneOf: [
                  {
                    title: 'External Outbound Cluster Link [SASL/SCRAM]',
                    const: 'external_outbound_sasl_scram',
                  },
                  {
                    title: 'External Outbound Cluster Link [Unauthenticated Plaintext]',
                    const: 'external_outbound_plaintext',
                  },
                  { title: 'Jump Cluster', const: 'jump_cluster' },
                ],
              },
            },
            required: ['private_migration_method'],
          },
          uiSchema: {
            private_migration_method: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'external_outbound_cluster_linking_inputs',
              guard: 'use_external_outbound_sasl_scram',
              actions: 'save_step_data',
            },
            {
              target: 'external_outbound_cluster_linking_plaintext_inputs',
              guard: 'use_external_outbound_plaintext',
              actions: 'save_step_data',
            },
            {
              target: 'private_link_internet_gateway_question',
              guard: 'use_jump_clusters',
              actions: 'save_step_data',
            },
          ],
          BACK: {
            target: 'target_cluster_type_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      external_outbound_cluster_linking_inputs: {
        meta: {
          title: 'Private Migration | External Outbound Cluster Linking [SASL/SCRAM]',
          description: 'Enter configuration details for your external outbound cluster linking',
          schema: {
            type: 'object',
            properties: {
              ...targetClusterProperties(),
              ...jumpClusterTargetProperties(),
              use_jump_clusters: {
                type: 'boolean',
                title: 'Use jump clusters',
                default: false,
              },
              ext_outbound_subnet_id: {
                type: 'string',
                title: 'Subnet ID',
                description:
                  'MSK broker 1 subnet ID is used by default for the external outbound cluster linking.',
                default:
                  cluster?.aws_client_information?.cluster_networking?.subnet_ids?.[0] ||
                  'failed to retrieve subnet ID from statefile.',
              },
              ext_outbound_security_group_id: {
                type: 'string',
                title: 'Security Group ID',
                description:
                  'MSK cluster security group ID is used by default for the external outbound cluster linking.',
                default:
                  cluster?.aws_client_information?.cluster_networking?.security_groups?.[0] ||
                  'failed to retrieve security group ID from statefile.',
              },
              source_region: {
                type: 'string',
                title: 'MSK Region',
                default: cluster?.region || 'failed to retrieve AWS region from statefile.',
              },
              source_cluster_id: {
                type: 'string',
                title: 'MSK Cluster ID',
                default:
                  cluster?.kafka_admin_client_information?.cluster_id ||
                  'failed to retrieve MSK cluster ID from statefile.',
              },
              source_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers',
                default:
                  cluster?.aws_client_information?.bootstrap_brokers
                    ?.BootstrapBrokerStringSaslScram ||
                  'failed to retrieve MSK SASL/SCRAM bootstrap servers (public) from statefile.',
              },
              source_sasl_scram_mechanism: {
                type: 'string',
                title: 'Source SASL/SCRAM Mechanism',
                default: cluster?.kafka_admin_client_information?.sasl_mechanism || 'SCRAM-SHA-512',
              },
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
                default:
                  cluster?.aws_client_information?.cluster_networking?.vpc_id ||
                  'failed to retrieve VPC ID from statefile.',
              },
              source_kafka_brokers: {
                type: 'array',
                title: 'AWS Kafka Brokers',
                default: awsKafkaBrokers.length > 0 ? awsKafkaBrokers : undefined,
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
            required: [
              'cluster_link_name',
              'target_environment_id',
              'target_cluster_id',
              'target_rest_endpoint',
              'ext_outbound_subnet_id',
              'ext_outbound_security_group_id',
              'source_region',
              'vpc_id',
              'source_cluster_id',
              'source_sasl_scram_bootstrap_servers',
              'source_kafka_brokers',
            ],
          },
          uiSchema: {
            ...targetClusterUiSchema(),
            ...jumpClusterTargetUiSchema(),
            use_jump_clusters: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            ext_outbound_subnet_id: {
              'ui:placeholder': 'e.g., subnet-xxxxxx',
            },
            ext_outbound_security_group_id: {
              'ui:placeholder': 'e.g., sg-xxxxxx',
            },
            source_region: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_cluster_id: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_sasl_scram_bootstrap_servers: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_sasl_scram_mechanism: {
              'ui:disabled': true,
            },
            vpc_id: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_kafka_brokers: {
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
      external_outbound_cluster_linking_plaintext_inputs: {
        meta: {
          title: 'Private Migration | External Outbound Cluster Linking [Unauthenticated Plaintext]',
          description:
            'Enter configuration details for your external outbound cluster linking using unauthenticated plaintext (port 9092)',
          schema: {
            type: 'object',
            properties: {
              use_jump_clusters: {
                type: 'boolean',
                title: 'Use jump clusters',
                default: false,
              },
              cluster_link_name: {
                type: 'string',
                title: 'Cluster Link Name (created during migration)',
              },
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
              ext_outbound_subnet_id: {
                type: 'string',
                title: 'Subnet ID',
                description:
                  'MSK broker 1 subnet ID is used by default for the external outbound cluster linking.',
                default:
                  cluster?.aws_client_information?.cluster_networking?.subnet_ids?.[0] ||
                  'failed to retrieve subnet ID from statefile.',
              },
              ext_outbound_security_group_id: {
                type: 'string',
                title: 'Security Group ID',
                description:
                  'MSK cluster security group ID is used by default for the external outbound cluster linking.',
                default:
                  cluster?.aws_client_information?.cluster_networking?.security_groups?.[0] ||
                  'failed to retrieve security group ID from statefile.',
              },
              source_region: {
                type: 'string',
                title: 'MSK Region',
                default: cluster?.region || 'failed to retrieve AWS region from statefile.',
              },
              source_cluster_id: {
                type: 'string',
                title: 'MSK Cluster ID',
                default:
                  cluster?.kafka_admin_client_information?.cluster_id ||
                  'failed to retrieve MSK cluster ID from statefile.',
              },
              source_plaintext_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers (Plaintext)',
                default:
                  cluster?.aws_client_information?.bootstrap_brokers?.BootstrapBrokerString ||
                  'failed to retrieve MSK plaintext bootstrap servers from statefile.',
              },
              jump_cluster_auth_type: {
                type: 'string',
                title: 'Auth Type',
                default: 'plaintext',
              },
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
                default:
                  cluster?.aws_client_information?.cluster_networking?.vpc_id ||
                  'failed to retrieve VPC ID from statefile.',
              },
              source_kafka_brokers: {
                type: 'array',
                title: 'AWS Kafka Brokers',
                default:
                  awsKafkaBrokers.length > 0
                    ? awsKafkaBrokers.map((b: any) => ({
                        ...b,
                        endpoints: b.endpoints?.map((e: any) => ({ ...e, port: 9092 })) || [],
                      }))
                    : undefined,
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
            required: [
              'cluster_link_name',
              'target_environment_id',
              'target_cluster_id',
              'target_rest_endpoint',
              'ext_outbound_subnet_id',
              'ext_outbound_security_group_id',
              'source_region',
              'vpc_id',
              'source_cluster_id',
              'source_plaintext_bootstrap_servers',
              'source_kafka_brokers',
            ],
          },
          uiSchema: {
            use_jump_clusters: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            cluster_link_name: {
              'ui:placeholder': 'e.g., msk-to-cc-migration-link',
            },
            target_environment_id: {
              'ui:placeholder': 'e.g., env-xxxxxx',
            },
            target_cluster_id: {
              'ui:placeholder': 'e.g., lkc-xxxxxx',
            },
            target_rest_endpoint: {
              'ui:placeholder': 'e.g., https://xxx.xxx.aws.confluent.cloud:443',
            },
            ext_outbound_subnet_id: {
              'ui:placeholder': 'e.g., subnet-xxxxxx',
            },
            ext_outbound_security_group_id: {
              'ui:placeholder': 'e.g., sg-xxxxxx',
            },
            source_region: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_cluster_id: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_plaintext_bootstrap_servers: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            jump_cluster_auth_type: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            vpc_id: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            source_kafka_brokers: {
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
      private_link_internet_gateway_question: {
        meta: {
          title: 'Private Migration | Private Link - Internet Gateway',
          description:
            'When migrating data from MSK to Confluent Cloud over a private network, a jump cluster is required and some dependencies will need to be installed on these on the jump cluster brokers from the internet.',
          schema: {
            type: 'object',
            properties: {
              has_existing_internet_gateway: {
                type: 'boolean',
                title: 'Does your MSK VPC network have an existing internet gateway?',
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
          BACK: [
            {
              target: 'private_migration_method_question',
              guard: 'came_from_private_migration_method_question',
              actions: 'undo_save_step_data',
            },
            {
              target: 'target_cluster_type_question',
              guard: 'came_from_target_cluster_type_question',
              actions: 'undo_save_step_data',
            },
          ],
        },
      },
      jump_cluster_networking_inputs: {
        meta: {
          title: 'Private Migration | Jump Cluster - Configuration',
          description: 'Enter configuration details for your jump cluster networking',
          schema: {
            type: 'object',
            properties: {
              use_jump_clusters: {
                type: 'boolean',
                title: 'Use jump clusters',
                default: true,
              },
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
                default:
                  cluster?.aws_client_information?.cluster_networking?.vpc_id ||
                  'failed to retrieve VPC ID from statefile.',
              },
              jump_cluster_instance_type: {
                type: 'string',
                title: 'Instance Type',
                default: instanceType.replace('kafka.', ''), // Remove the `kafka.` prefix from MSK instance type to get its EC2 equivalent.
              },
              jump_cluster_broker_storage: {
                type: 'number',
                title: 'Storage per Broker (GB)',
                default:
                  cluster?.aws_client_information?.msk_cluster_config?.Provisioned
                    ?.BrokerNodeGroupInfo?.StorageInfo?.EbsStorageInfo?.VolumeSize || 500,
              },
              jump_cluster_broker_subnet_cidr: {
                type: 'array',
                title: 'Broker Subnet CIDR Range',
                description:
                  'The number of subnets to create determines the number of jump cluster brokers that will be created.',
                items: {
                  type: 'string',
                },
                minItems:
                  cluster?.aws_client_information?.nodes?.filter(
                    (node) => node.NodeType === 'BROKER'
                  ).length || 3,
                default: ['', '', ''],
              },
              jump_cluster_setup_host_subnet_cidr: {
                type: 'string',
                title: 'Jump Cluster Setup Host CIDR',
                description:
                  'The subnet CIDR range for EC2 instance that will provision the jump cluster instances.',
              },
            },
            required: [
              'vpc_id',
              'jump_cluster_instance_type',
              'jump_cluster_broker_storage',
              'jump_cluster_broker_subnet_cidr',
              'jump_cluster_setup_host_subnet_cidr',
            ],
          },
          uiSchema: {
            use_jump_clusters: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            vpc_id: {
              'ui:disabled': true,
            },
            jump_cluster_instance_type: {
              'ui:placeholder': 'e.g., m5.large',
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
          NEXT: {
            target: 'jump_cluster_authentication_question',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'private_link_internet_gateway_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      jump_cluster_authentication_question: {
        meta: {
          title: 'Private Migration | Jump Cluster - Authentication',
          description: 'How will the jump cluster authenticate to the MSK cluster?',
          schema: {
            type: 'object',
            properties: {
              jump_cluster_auth_type: {
                type: 'string',
                title: 'MSK Jump Cluster Authentication Type',
                oneOf: [
                  { title: 'SASL/SCRAM', const: 'sasl_scram' },
                  { title: 'IAM', const: 'iam' },
                ],
              },
            },
            required: ['jump_cluster_auth_type'],
          },
          uiSchema: {
            jump_cluster_auth_type: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'jump_cluster_authentication_sasl_scram',
              guard: 'selected_jump_cluster_authentication_sasl_scram',
              actions: 'save_step_data',
            },
            {
              target: 'jump_cluster_authentication_iam',
              guard: 'selected_jump_cluster_authentication_iam',
              actions: 'save_step_data',
            },
          ],
          BACK: {
            target: 'jump_cluster_networking_inputs',
            actions: 'undo_save_step_data',
          },
        },
      },
      jump_cluster_authentication_sasl_scram: {
        meta: {
          title: 'Private Migration | Confluent Cloud & Cluster Link Configuration',
          description: 'Configure the Confluent Cloud target and cluster link details for your migration.',
          schema: {
            type: 'object',
            properties: {
              source_cluster_id: {
                type: 'string',
                title: 'MSK Cluster ID',
                default:
                  cluster?.kafka_admin_client_information?.cluster_id ||
                  'failed to retrieve MSK cluster ID from statefile.',
              },
              source_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers',
                default:
                  cluster?.aws_client_information?.bootstrap_brokers
                    ?.BootstrapBrokerStringSaslScram ||
                  'failed to retrieve MSK SASL/SCRAM bootstrap servers (private) from statefile.',
              },
              source_sasl_scram_mechanism: {
                type: 'string',
                title: 'Source SASL/SCRAM Mechanism',
                default: cluster?.kafka_admin_client_information?.sasl_mechanism || 'SCRAM-SHA-512',
              },
              source_region: {
                type: 'string',
                title: 'MSK Region',
                default: cluster?.region || 'failed to retrieve AWS region from statefile.',
              },
              ...targetClusterProperties(),
              ...jumpClusterTargetProperties(),
            },
            required: [
              'source_cluster_id',
              'source_sasl_scram_bootstrap_servers',
              'source_region',
              'target_environment_id',
              'target_cluster_id',
              'target_rest_endpoint',
              'target_bootstrap_endpoint',
              'cluster_link_name',
            ],
          },
          uiSchema: {
            source_cluster_id: {
              'ui:disabled': true,
            },
            source_sasl_scram_bootstrap_servers: {
              'ui:disabled': true,
            },
            source_sasl_scram_mechanism: {
              'ui:disabled': true,
            },
            source_region: {
              'ui:disabled': true,
            },
            ...targetClusterUiSchema(),
            ...jumpClusterTargetUiSchema(),
          },
        },
        on: {
          NEXT: {
            target: 'confirmation',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'jump_cluster_authentication_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      jump_cluster_authentication_iam: {
        meta: {
          title: 'Private Migration | Confluent Cloud & Cluster Link Configuration',
          description: 'Configure the Confluent Cloud target and cluster link details for your migration.',
          schema: {
            type: 'object',
            properties: {
              source_cluster_id: {
                type: 'string',
                title: 'MSK Cluster ID',
                default:
                  cluster?.kafka_admin_client_information?.cluster_id ||
                  'failed to retrieve MSK cluster ID from statefile.',
              },
              source_sasl_iam_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers',
                default:
                  cluster?.aws_client_information?.bootstrap_brokers
                    ?.BootstrapBrokerStringSaslIam ||
                  'failed to retrieve MSK IAM bootstrap servers (private) from statefile.',
              },
              source_region: {
                type: 'string',
                title: 'MSK Region',
                default: cluster?.region || 'failed to retrieve AWS region from statefile.',
              },
              ...targetClusterProperties(),
              ...jumpClusterTargetProperties(),
              jump_cluster_iam_auth_role_name: {
                type: 'string',
                title: 'Instance Role Name',
                description:
                  'The name of the pre-configured IAM role that will be used to authenticate the cluster link between MSK and the jump cluster.',
              },
            },
            required: [
              'source_cluster_id',
              'source_sasl_iam_bootstrap_servers',
              'source_region',
              'target_environment_id',
              'target_cluster_id',
              'target_rest_endpoint',
              'target_bootstrap_endpoint',
              'cluster_link_name',
              'jump_cluster_iam_auth_role_name',
            ],
          },
          uiSchema: {
            source_cluster_id: {
              'ui:disabled': true,
            },
            source_sasl_iam_bootstrap_servers: {
              'ui:disabled': true,
            },
            source_region: {
              'ui:disabled': true,
            },
            ...targetClusterUiSchema(),
            ...jumpClusterTargetUiSchema(),
            jump_cluster_iam_auth_role_name: {
              uiwidget: 'input',
            },
          },
        },
        on: {
          NEXT: {
            target: 'confirmation',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'jump_cluster_authentication_question',
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
              target: 'external_outbound_cluster_linking_inputs',
              guard: 'came_from_external_outbound_cluster_linking_inputs',
              actions: 'undo_save_step_data',
            },
            {
              target: 'external_outbound_cluster_linking_plaintext_inputs',
              guard: 'came_from_external_outbound_cluster_linking_plaintext_inputs',
              actions: 'undo_save_step_data',
            },
            {
              target: 'jump_cluster_authentication_sasl_scram',
              guard: 'came_from_jump_cluster_authentication_sasl_scram',
              actions: 'undo_save_step_data',
            },
            {
              target: 'jump_cluster_authentication_iam',
              guard: 'came_from_jump_cluster_authentication_iam',
              actions: 'undo_save_step_data',
            },
          ],
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
      has_public_brokers: ({ event }) => {
        return event.data?.has_public_brokers === true
      },
      has_private_cc_endpoints: ({ event }) => {
        return event.data?.has_public_brokers === false
      },
      use_jump_clusters: ({ event }) => {
        return event.data?.private_migration_method === 'jump_cluster'
      },
      use_external_outbound_sasl_scram: ({ event }) => {
        return event.data?.private_migration_method === 'external_outbound_sasl_scram'
      },
      use_external_outbound_plaintext: ({ event }) => {
        return event.data?.private_migration_method === 'external_outbound_plaintext'
      },
      came_from_public_cluster_link_inputs: ({ context }) => {
        return context.previousStep === 'public_cluster_link_inputs'
      },
      selected_jump_cluster_authentication_sasl_scram: ({ event }) => {
        return event.data?.jump_cluster_auth_type === 'sasl_scram'
      },
      selected_jump_cluster_authentication_iam: ({ event }) => {
        return event.data?.jump_cluster_auth_type === 'iam'
      },
      target_cluster_is_enterprise: ({ event }) => {
        return event.data?.target_cluster_type === 'enterprise'
      },
      target_cluster_is_dedicated: ({ event }) => {
        return event.data?.target_cluster_type === 'dedicated'
      },
      came_from_private_migration_method_question: ({ context }) => {
        return context.previousStep === 'private_migration_method_question'
      },
      came_from_target_cluster_type_question: ({ context }) => {
        return context.previousStep === 'target_cluster_type_question'
      },
      came_from_external_outbound_cluster_linking_inputs: ({ context }) => {
        return context.previousStep === 'external_outbound_cluster_linking_inputs'
      },
      came_from_external_outbound_cluster_linking_plaintext_inputs: ({ context }) => {
        return context.previousStep === 'external_outbound_cluster_linking_plaintext_inputs'
      },
      came_from_jump_cluster_authentication_sasl_scram: ({ context }) => {
        return context.previousStep === 'msk_jump_cluster_authentication_sasl_scram'
      },
      came_from_jump_cluster_authentication_iam: ({ context }) => {
        return context.previousStep === 'jump_cluster_authentication_iam'
      },
    },
    actions: {
      save_step_data: 'save_step_data',
      undo_save_step_data: 'undo_save_step_data',
    },
  }
}
