import type { WizardConfig } from './types'
import { getClusterDataByArn } from '@/stores/store'

export const createMirrorTopicsMigrationScriptsWizardConfig = (clusterArn: string): WizardConfig => {
  const cluster = getClusterDataByArn(clusterArn)

  const topics = cluster?.kafka_admin_client_information?.topics?.details || []
  const topicNames = topics.filter((topic: any) => !topic.name.startsWith('__')).map((topic: any) => topic.name)
  const topicEnumValues = topicNames.length > 0 ? topicNames : ['No topics available']

  console.log(topicEnumValues)

  return {
    id: 'mirror-topics-migration-scripts-wizard',
    title: 'Mirror Topics Migration Scripts Wizard',
    description: 'Configure your mirror topics migration scripts',
    apiEndpoint: '/assets/migration-scripts/topics',
    initial: 'target_cluster_inputs',

    states: {
      target_cluster_inputs: {
        meta: {
          title: 'Mirror Topics | Target Cluster Inputs',
          schema: {
            type: 'object',
            properties: {
              target_cluster_id: {
                type: 'string',
                title: 'Confluent Cloud Cluster ID',
              },
              target_cluster_rest_endpoint: {
                type: 'string',
                title: 'Confluent Cloud Cluster REST Endpoint',
              },
              cluster_link_name: {
                type: 'string',
                title: 'Cluster Link Name',
              },
            },
            required: ['target_cluster_id', 'target_cluster_rest_endpoint', 'cluster_link_name'],
          },
          uiSchema: {
            target_cluster_id: {
              'ui:placeholder': 'e.g., lkc-xxxxxx',
            },
            target_cluster_rest_endpoint: {
              'ui:placeholder': 'e.g., https://xxx.xxx.aws.confluent.cloud:443',
            },
            cluster_link_name: {
              'ui:placeholder': 'e.g., msk-to-cc-migration-link',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'topic_selection',
              actions: 'save_step_data',
            },
          ],
        },
      },
      topic_selection: {
        meta: {
          title: 'Select Topics to Mirror',
          description: `Select the topics you wish to generate mirror topic scripts for from ${cluster?.name}.`,
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
              'ui:options': {
                enum: topicEnumValues,
              },
            },
          },
        },
        on: {
          NEXT: {
            target: 'confirmation',
            actions: 'save_step_data',
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
              target: 'topic_selection',
              actions: 'undo_save_step_data',
            },
          ],
        },
      },
      complete: {
        type: 'final',
        meta: {
          title: 'Configuration Complete',
          message: 'Your mirror topics migration scripts are ready to be processed...',
        },
      },
    },

    guards: {
      came_from_topic_selection: ({ context }) => {
        return context.previousStep === 'topic_selection'
      },
    },

    actions: {
      save_step_data: 'save_step_data',
      undo_save_step_data: 'undo_save_step_data',
    },
  }
}
