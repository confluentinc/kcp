import type { WizardConfig } from './types'

interface ClusterData {
  cluster: any
  regionName: string
}

export const createMigrationScriptsWizardConfig = (
  clusterData: ClusterData
): WizardConfig => {
  // Extract data from the cluster
  const kafkaAdminInfo = clusterData.cluster?.kafka_admin_client_information || {}
  const awsClientInfo = clusterData.cluster?.aws_client_information || {}

  // Topics
  const topics = kafkaAdminInfo.topics?.details || []
  const topicNames = topics.filter((topic: any) => !topic.name.startsWith('__')).map((topic: any) => topic.name)
  const topicEnumValues = topicNames.length > 0 ? topicNames : ['No topics available']

  // ACLs - create unique identifiers for each ACL
  const acls = kafkaAdminInfo.acls || []
  const aclIdentifiers = acls.map(
    (acl: any) =>
      `${acl.ResourceType}:${acl.ResourceName} - ${acl.Principal} (${acl.Operation}, ${acl.PermissionType})`
  )
  const aclEnumValues = aclIdentifiers.length > 0 ? aclIdentifiers : ['No ACLs available']

  // Connectors
  const connectors = awsClientInfo.connectors || []
  const connectorNames = connectors.map((connector: any) => connector.connector_name)
  const connectorEnumValues =
    connectorNames.length > 0 ? connectorNames : ['No connectors available']

  // Schemas (placeholder for future implementation)
  const schemaEnumValues = ['Schema migration not yet implemented']

  return {
    id: 'migration-scripts-wizard',
    title: 'Migration Scripts Wizard',
    description: 'Generate migration scripts and automation tools',
    apiEndpoint: '/assets/migration-scripts',

    states: {
      migration_type: {
        meta: {
          title: 'Migration Type',
          description: 'What type of migration do you wish to perform?',
          schema: {
            type: 'object',
            properties: {
              migration_type: {
                type: 'string',
                enum: [
                  'Mirror Topics',
                  'Migrate ACLs',
                  'Migrate Connectors',
                  'Migrate Schemas',
                ],
                title: 'Migration Type',
                description: 'Select the type of migration you wish to perform',
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
          NEXT: [
            {
              target: 'selected_topics',
              guard: 'is_mirror_topics',
              actions: 'save_step_data',
            },
            {
              target: 'select_acls',
              guard: 'is_migrate_acls',
              actions: 'save_step_data',
            },
            {
              target: 'select_connectors',
              guard: 'is_migrate_connectors',
              actions: 'save_step_data',
            },
            {
              target: 'select_schemas',
              guard: 'is_migrate_schemas',
              actions: 'save_step_data',
            },
          ],
        },
      },

      selected_topics: {
        meta: {
          title: 'Select Topics to Mirror',
          description: `Select the topics you wish to generate mirror topic scripts for from ${clusterData.cluster.name}.`,
          schema: {
            type: 'object',
            properties: {
              selected_topics: {
                type: 'array',
                title: 'Topics',
                description: `Select one or more topics to mirror (${topicNames.length} topics available)`,
                items: {
                  type: 'string',
                  enum: topicEnumValues,
                },
                uniqueItems: true,
                minItems: 1,
              },
            },
            required: ['selected_topics'],
          },
          uiSchema: {
            selected_topics: {
              'ui:widget': 'checkboxes',
            },
          },
        },
        on: {
          NEXT: {
            target: 'mirror_topics_confluent_cloud_details',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'migration_type',
          },
        },
      },
      mirror_topics_confluent_cloud_details: {
        meta: {
          title: 'Confluent Cloud Details',
          description: 'Enter the details of the Confluent Cloud cluster you wish to mirror topics to.',
          schema: {
            type: 'object',
            properties: {
              cluster_link_name: {
                type: 'string',
                title: 'Cluster Link Name',
              },
              confluent_cloud_cluster_id: {
                type: 'string',
                title: 'Confluent Cloud Cluster ID',
              },
              confluent_cloud_cluster_rest_endpoint: {
                type: 'string',
                title: 'Confluent Cloud Cluster REST Endpoint',
              },
            },
            required: ['cluster_link_name', 'confluent_cloud_cluster_id', 'confluent_cloud_cluster_rest_endpoint'],
          },
          uiSchema: {
            cluster_link_name: {
              'ui:placeholder': 'e.g., msk-to-cc-link',
            },
            confluent_cloud_cluster_id: {
              'ui:placeholder': 'e.g., cluster-xxxxx',
            },
            confluent_cloud_cluster_rest_endpoint: {
              'ui:placeholder': 'e.g., https://api.confluent.cloud',
            },
          },
        },
        on: {
          NEXT: {
            target: 'complete',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'selected_topics',
          },
        },
      },

      select_acls: {
        meta: {
          title: 'Select the principals you wish to migrate',
          description: `Select the principals and their associated ACLs you wish to migrate from ${clusterData.cluster.name}.`,
          schema: {
            type: 'object',
            properties: {
              selected_acls: {
                type: 'array',
                title: 'ACLs',
                description: `Select one or more ACLs to migrate (${acls.length} ACLs available)`,
                items: {
                  type: 'string',
                  enum: aclEnumValues,
                },
                uniqueItems: true,
                minItems: 1,
              },
            },
            required: ['selected_acls'],
          },
          uiSchema: {
            selected_acls: {
              'ui:widget': 'checkboxes',
            },
          },
        },
        on: {
          NEXT: {
            target: 'complete',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'migration_type',
          },
        },
      },

      select_connectors: {
        meta: {
          title: 'Select Connectors to Migrate',
          description: `Select the connectors you wish to migrate from ${clusterData.cluster.name}.`,
          schema: {
            type: 'object',
            properties: {
              selected_connectors: {
                type: 'array',
                title: 'Connectors',
                description: `Select one or more connectors to migrate (${connectors.length} connectors available)`,
                items: {
                  type: 'string',
                  enum: connectorEnumValues,
                },
                uniqueItems: true,
                minItems: 1,
              },
            },
            required: ['selected_connectors'],
          },
          uiSchema: {
            selected_connectors: {
              'ui:widget': 'checkboxes',
            },
          },
        },
        on: {
          NEXT: {
            target: 'complete',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'migration_type',
          },
        },
      },

      select_schemas: {
        meta: {
          title: 'Select Schemas to Migrate',
          description: `Select the schemas you wish to migrate from ${clusterData.cluster.name}.`,
          schema: {
            type: 'object',
            properties: {
              selected_schemas: {
                type: 'array',
                title: 'Schemas',
                description: 'Schema migration is not yet implemented',
                items: {
                  type: 'string',
                  enum: schemaEnumValues,
                },
                uniqueItems: true,
              },
            },
            required: ['selected_schemas'],
          },
          uiSchema: {
            selected_schemas: {
              'ui:widget': 'checkboxes',
            },
          },
        },
        on: {
          NEXT: {
            target: 'complete',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'migration_type',
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

    guards: {
      is_mirror_topics: ({ event }) => {
        return event.data?.migration_type === 'Mirror Topics'
      },
      is_migrate_acls: ({ event }) => {
        return event.data?.migration_type === 'Migrate ACLs'
      },
      is_migrate_connectors: ({ event }) => {
        return event.data?.migration_type === 'Migrate Connectors'
      },
      is_migrate_schemas: ({ event }) => {
        return event.data?.migration_type === 'Migrate Schemas'
      },
    },

    actions: {
      save_step_data: 'save_step_data',
    },
  }
}

// Backward compatibility: export a default config for when cluster data is not available
export const migrationScriptsWizardConfig = createMigrationScriptsWizardConfig({
  cluster: { name: 'Unknown Cluster', kafka_admin_client_information: null },
  regionName: 'Unknown Region',
})
