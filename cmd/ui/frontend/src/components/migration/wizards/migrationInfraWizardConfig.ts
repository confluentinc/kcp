import type { WizardConfig } from './types'
import { getClusterDataByArn } from '@/stores/store'

export const createMigrationInfraWizardConfig = (clusterArn: string): WizardConfig => {
  const cluster = getClusterDataByArn(clusterArn)

  const instanceType = cluster?.metrics?.metadata?.instance_type || 'kafka.m5.xlarge'

  const brokerNodes = cluster?.aws_client_information?.nodes?.filter(
    (node: any) => node.NodeType === 'BROKER' && node.BrokerNodeInfo
  ) || []

  const subnets = cluster?.aws_client_information?.cluster_networking?.subnets || []

  const awsKafkaBrokers = brokerNodes.map((node: any) => {
    const brokerId = node.BrokerNodeInfo.BrokerId
    const matchingSubnet = subnets.find((subnet: any) => subnet.subnet_msk_broker_id === brokerId)
    
    return {
      broker_id: brokerId?.toString() || '',
      subnet_id: node.BrokerNodeInfo.ClientSubnet || '',
      endpoints: node.BrokerNodeInfo.Endpoints?.map((endpoint: string) => ({
        host: endpoint,
        port: 9096,
        ip: matchingSubnet?.private_ip_address || ''
      })) || []
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
          description: 'When migrating from MSK to Confluent Cloud, you can choose to use public or private networking. Public networking is the default and requires no additional configuration. Private networking is more complex involving private linking and jump clusters.',
          schema: {
            type: 'object',
            properties: {
              has_public_msk_brokers: {
                type: 'boolean',
                title: 'Are your MSK brokers publicly accessible?',
                oneOf: [
                  { title: 'Yes', const: true },
                  { title: 'No', const: false },
                ],
              },
            },
            required: ['has_public_msk_brokers'],
          },
          uiSchema: {
            has_public_msk_brokers: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'public_cluster_link_inputs',
              guard: 'has_public_msk_brokers',
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
                title: 'MSK Cluster ID',
                default: cluster?.kafka_admin_client_information?.cluster_id || 'failed to retrieve MSK cluster ID from statefile.'
              },
              msk_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers',
                default: cluster?.aws_client_information?.bootstrap_brokers?.BootstrapBrokerStringPublicSaslScram || 'failed to retrieve MSK SASL/SCRAM bootstrap servers (public) from statefile.'
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
              'ui:disabled': true,
            },
            msk_sasl_scram_bootstrap_servers: {
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
      private_migration_method_question: {
        meta: {
          title: 'Private Migration | Method',
          description: 'MSK to Confluent Cloud migrations can be performed through either jump clusters or external outbound cluster linking.',
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
              target: 'private_link_existing_question',
              guard: 'use_jump_clusters',
              actions: 'save_step_data',
            }
          ],
          BACK: {
            target: 'confluent_cloud_endpoints_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      external_outbound_cluster_linking_inputs: {
        meta: {
          title: 'Private Migration | External Outbound Cluster Linking',
          description: 'Enter configuration details for your external outbound cluster linking',
          schema: {
            type: 'object',
            properties: {
              cluster_link_name: {
                type: 'string',
                title: 'Confluent Cloud Cluster Link Name',
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
                description: 'MSK broker 1 subnet ID is used by default for the external outbound cluster linking.',
                default: cluster?.aws_client_information?.cluster_networking?.subnet_ids?.[0] || 'failed to retrieve subnet ID from statefile.'
              },
              ext_outbound_security_group_id: {
                type: 'string',
                title: 'Security Group ID',
                description: 'MSK cluster security group ID is used by default for the external outbound cluster linking.',
                default: cluster?.aws_client_information?.cluster_networking?.security_groups?.[0] || 'failed to retrieve security group ID from statefile.'
              },
              msk_region: {
                type: 'string',
                title: 'MSK Region',
                default: cluster?.region || 'failed to retrieve AWS region from statefile.'
              },
              msk_cluster_id: {
                type: 'string',
                title: 'MSK Cluster ID',
                default: cluster?.kafka_admin_client_information?.cluster_id || 'failed to retrieve MSK cluster ID from statefile.'
              },
              msk_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers',
                default: cluster?.aws_client_information?.bootstrap_brokers?.BootstrapBrokerStringSaslScram || 'failed to retrieve MSK SASL/SCRAM bootstrap servers (public) from statefile.'
              },
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
                default: cluster?.aws_client_information?.cluster_networking?.vpc_id || 'failed to retrieve VPC ID from statefile.'
              },
              aws_kafka_brokers: {
                type: 'array',
                title: 'AWS Kafka Brokers',
                default: awsKafkaBrokers.length > 0 ? awsKafkaBrokers : undefined,
                items: {
                  type: "object",
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
                          }
                        }
                      }
                    }
                  }
                }
              }
            },
            required: ['cluster_link_name', 'target_environment_id', 'target_cluster_id', 'target_rest_endpoint', 'ext_outbound_subnet_id', 'ext_outbound_security_group_id', 'msk_region', 'vpc_id', 'msk_cluster_id', 'msk_sasl_scram_bootstrap_servers', 'aws_kafka_brokers'],
          },
          uiSchema: {
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
            msk_region: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            msk_cluster_id: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            msk_sasl_scram_bootstrap_servers: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
            },
            vpc_id: {
              'ui:widget': 'hidden',
              'ui:disabled': true,
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
      private_link_existing_question: {
        meta: {
          title: 'Private Migration | Private Link',
          description: 'AWS VPCs allow only one private hosted zone per domain per VPC. Therefore, KCP needs to know if a Private Link connection already exists to the Confluent Cloud cluster.',
          schema: {
            type: 'object',
            properties: {
              has_existing_private_link: {
                type: 'boolean',
                title: 'Do you have a currently established Private Link connection to the Confluent Cloud cluster?',
                oneOf: [
                  { title: 'Yes', const: true },
                  { title: 'No', const: false },
                ],
              },
            },
            required: ['has_existing_private_link'],
          },
          uiSchema: {
            has_existing_private_link: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: {
            target: 'private_link_subnets_question',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'private_migration_method_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      private_link_subnets_question: {
        meta: {
          title: 'Private Migration | Private Link - Subnets',
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
            target: 'private_link_existing_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      private_link_reuse_existing_subnets: {
        meta: {
          title: 'Private Migration | Private Link - Reuse Existing Subnets',
          description: 'Reuse the existing subnets from your MSK cluster for setting up the private link between Confluent Cloud and the AWS VPC.',
          schema: {
            type: 'object',
            properties: {
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
                default: cluster?.aws_client_information?.cluster_networking?.vpc_id || 'failed to retrieve VPC ID from statefile.'
              },
              private_link_existing_subnet_ids: {
                type: 'array',
                title: 'Existing subnet IDs',
                description: 'Retrieved from the statefile, these can be modified to other existing subnet IDs in the MSK VPC.',
                items: {
                  type: 'string',
                },
                minItems: 3,
                maxItems: 3,
                default: cluster?.aws_client_information?.cluster_networking?.subnet_ids?.slice(0, 3) || 
                cluster?.aws_client_information?.cluster_networking?.subnets?.slice(0, 3).map(s => s.subnet_id) ||
                ['failed to retrieve existing subnet IDs from statefile.', '', ''],
              },
            },
            required: ['vpc_id', 'private_link_existing_subnet_ids'],
          },
          uiSchema: {
            vpc_id: {
              'ui:disabled': true,
            },
            private_link_existing_subnet_ids: {
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
            target: 'private_link_internet_gateway_question',
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
          description: 'Create new subnets for setting up the private link between Confluent Cloud and the AWS VPC.',
          schema: {
            type: 'object',
            properties: {
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
                default: cluster?.aws_client_information?.cluster_networking?.vpc_id || 'failed to retrieve VPC ID from statefile.'
              },
              private_link_new_subnets_cidr: {
                type: 'array',
                title: 'New subnet CIDR ranges',
                items: {
                  type: 'string',
                },
                minItems: 3,
                maxItems: 3,
                default: ['', '', ''],
              },
            },
            required: ['vpc_id', 'private_link_new_subnets_cidr'],
          },
          uiSchema: {
            vpc_id: {
              'ui:disabled': true,
            },
            private_link_new_subnets_cidr: {
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
          description: 'When migrating data from MSK to Confluent Cloud over a private network, a jump cluster is required and some dependencies will need to be installed on these on the jump cluster brokers from the internet.',
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
                title: 'VPC ID',
                default: cluster?.aws_client_information?.cluster_networking?.vpc_id || 'failed to retrieve VPC ID from statefile.'
              },
              jump_cluster_instance_type: {
                type: 'string',
                title: 'Instance Type',
                default: instanceType.replace('kafka.', '') // Remove the `kafka.` prefix from MSK instance type to get its EC2 equivalent.
              },
              jump_cluster_broker_storage: {
                type: 'number',
                title: 'Broker Storage per Broker (GB)',
                default: cluster?.aws_client_information?.msk_cluster_config?.Provisioned?.BrokerNodeGroupInfo?.StorageInfo?.EbsStorageInfo?.VolumeSize || 500
              },
              jump_cluster_broker_subnet_cidr: {
                type: 'array',
                title: 'Broker Subnet CIDR Range',
                description: 'The number of subnets to create determines the number of jump cluster brokers that will be created.',
                items: {
                  type: 'string',
                },
                minItems: cluster?.aws_client_information?.nodes?.filter(node => node.NodeType === 'BROKER').length || 3,
                default: ['', '', ''],
              },
              jump_cluster_setup_host_subnet_cidr: {
                type: 'string',
                title: 'Jump Cluster Setup Host',
                description: 'The subnet CIDR range for EC2 instance that will provision the jump cluster instances.',
              }
            },
            required: ['vpc_id', 'jump_cluster_instance_type', 'jump_cluster_broker_storage', 'jump_cluster_broker_subnet_cidr', 'jump_cluster_setup_host_subnet_cidr'],
            },
          uiSchema: {
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
            }
          },
        },
        on: {
          NEXT: [
            {
              target: 'msk_jump_cluster_authentication_sasl_scram',
              guard: 'selected_msk_jump_cluster_authentication_sasl_scram',
              actions: 'save_step_data',
            },
            {
              target: 'msk_jump_cluster_authentication_iam',
              guard: 'selected_msk_jump_cluster_authentication_iam',
              actions: 'save_step_data',
            },
          ],
          BACK: {
            target: 'jump_cluster_networking_inputs',
            actions: 'undo_save_step_data',
          },
        },
      },
      msk_jump_cluster_authentication_sasl_scram: {
        meta: {
          title: 'Private Migration | Jump Cluster - Authentication (SASL/SCRAM)',
          description: 'How will the jump cluster authenticate to the MSK cluster?',
          schema: {
            type: 'object',
            properties: {
              msk_cluster_id: {
                type: 'string',
                title: 'MSK Cluster ID',
                default: cluster?.kafka_admin_client_information?.cluster_id || 'failed to retrieve MSK cluster ID from statefile.'
              },
              msk_sasl_scram_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers',
                default: cluster?.aws_client_information?.bootstrap_brokers?.BootstrapBrokerStringSaslScram || 'failed to retrieve MSK SASL/SCRAM bootstrap servers (private) from statefile.'
              },
              msk_region: {
                type: 'string',
                title: 'MSK Region',
                default: cluster?.region || 'failed to retrieve AWS region from statefile.'
              },
              target_cluster_id: {
                type: 'string',
                title: 'Confluent Cloud Cluster ID'
              },
              target_rest_endpoint: {
                type: 'string',
                title: 'Confluent Cloud Cluster REST Endpoint'
              },
              target_bootstrap_endpoint: {
                type: 'string',
                title: 'Confluent Cloud Cluster Bootstrap Endpoint'
              },
              cluster_link_name: {
                type: 'string',
                title: 'Confluent Cloud Cluster Link Name'
              },
            },
            required: ['msk_cluster_id', 'msk_sasl_scram_bootstrap_servers', 'msk_region', 'target_cluster_id', 'target_rest_endpoint', 'target_bootstrap_endpoint', 'cluster_link_name'],
          },
          uiSchema: {
            msk_cluster_id: {
              'ui:disabled': true,
            },
            msk_sasl_scram_bootstrap_servers: {
              'ui:disabled': true,
            },
            msk_region: {
              'ui:disabled': true,
            },
            target_cluster_id: {
              'ui:placeholder': 'e.g., lkc-xxxxxx',
            },
            target_rest_endpoint: {
              'ui:placeholder': 'e.g., https://xxx.xxx.aws.confluent.cloud:443',
            },
            target_bootstrap_endpoint: {
              'ui:placeholder': 'e.g., xxx.xxx.aws.confluent.cloud:9092',
            },
            cluster_link_name: {
              'ui:placeholder': 'e.g., msk-to-cc-migration-link',
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
      msk_jump_cluster_authentication_iam: {
        meta: {
          title: 'Private Migration | Jump Cluster - Authentication (IAM)',
          description: 'How will the jump cluster authenticate to the MSK cluster?',
          schema: {
            type: 'object',
            properties: {
              msk_cluster_id: {
                type: 'string',
                title: 'MSK Cluster ID',
                default: cluster?.kafka_admin_client_information?.cluster_id || 'failed to retrieve MSK cluster ID from statefile.'
              },
              msk_sasl_iam_bootstrap_servers: {
                type: 'string',
                title: 'MSK Bootstrap Servers',
                default: cluster?.aws_client_information?.bootstrap_brokers?.BootstrapBrokerStringSaslIam || 'failed to retrieve MSK IAM bootstrap servers (private) from statefile.'
              },
              msk_region: {
                type: 'string',
                title: 'MSK Region',
                default: cluster?.region || 'failed to retrieve AWS region from statefile.'
              },
              target_environment_id: {
                type: 'string',
                title: 'Confluent Cloud Environment ID'
              },
              target_cluster_id: {
                type: 'string',
                title: 'Confluent Cloud Cluster ID'
              },
              target_rest_endpoint: {
                type: 'string',
                title: 'Confluent Cloud Cluster REST Endpoint'
              },
              target_bootstrap_endpoint: {
                type: 'string',
                title: 'Confluent Cloud Cluster Bootstrap Endpoint'
              },
              cluster_link_name: {
                type: 'string',
                title: 'Confluent Cloud Cluster Link Name'
              },
              jump_cluster_iam_auth_role_name: {
                type: 'string',
                title: 'Instance Role Name',
                description: 'The name of the pre-configured IAM role that will be used to authenticate the cluster link between MSK and the jump cluster.'
              },
            },
            required: ['msk_cluster_id', 'msk_sasl_iam_bootstrap_servers', 'msk_region', 'target_environment_id', 'target_cluster_id', 'target_rest_endpoint', 'target_bootstrap_endpoint', 'cluster_link_name', 'jump_cluster_iam_auth_role_name'],
          },
          uiSchema: {
            msk_cluster_id: {
              'ui:disabled': true,
            },
            msk_sasl_iam_bootstrap_servers: {
              'ui:disabled': true,
            },
            msk_region: {
              'ui:disabled': true,
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
            target_bootstrap_endpoint: {
              'ui:placeholder': 'e.g., xxx.xxx.aws.confluent.cloud:9092',
            },
            cluster_link_name: {
              'ui:placeholder': 'e.g., msk-to-cc-migration-link',
            },
            jump_cluster_iam_auth_role_name: {
              'uiwidget': 'input',
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
              target: 'external_outbound_cluster_linking_inputs',
              guard: 'came_from_external_outbound_cluster_linking_inputs',
              actions: 'undo_save_step_data',
            },
            {
              target: 'msk_jump_cluster_authentication_sasl_scram',
              guard: 'came_from_msk_jump_cluster_authentication_sasl_scram',
              actions: 'undo_save_step_data',
            },
            {
              target: 'msk_jump_cluster_authentication_iam',
              guard: 'came_from_msk_jump_cluster_authentication_iam',
              actions: 'undo_save_step_data',
            },
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
      has_public_msk_brokers: ({ event}) => {
        return event.data?.has_public_msk_brokers === true
      },
      has_private_cc_endpoints: ({ event}) => {
        return event.data?.has_public_msk_brokers === false
      },
      use_jump_clusters: ({ event }) => {
        return event.data?.use_jump_clusters === true
      },
      use_external_outbound_cluster_linking: ({ event }) => {
        return event.data?.use_jump_clusters === false
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
      selected_msk_jump_cluster_authentication_sasl_scram: ({ event }) => {
        return event.data?.msk_jump_cluster_auth_type === 'sasl_scram'
      },
      selected_msk_jump_cluster_authentication_iam: ({ event }) => {
        return event.data?.msk_jump_cluster_auth_type === 'iam'
      },
      came_from_external_outbound_cluster_linking_inputs: ({ context }) => {
        return context.previousStep === 'external_outbound_cluster_linking_inputs'
      },
      came_from_msk_jump_cluster_authentication_sasl_scram: ({ context }) => {
        return context.previousStep === 'msk_jump_cluster_authentication_sasl_scram'
      },
      came_from_msk_jump_cluster_authentication_iam: ({ context }) => {
        return context.previousStep === 'msk_jump_cluster_authentication_iam'
      },
    },
    actions: {
      save_step_data: 'save_step_data',
      undo_save_step_data: 'undo_save_step_data',
    },
  }
}
