import type { WizardConfig } from './types'
import { getClusterDataBySourceType } from '@/stores/store'

const MODE_MIRROR = 'mirror'
const MODE_NEW = 'new'

export const createMirrorTopicsMigrationScriptsWizardConfig = (clusterKey: string, sourceType: 'msk' | 'osk' = 'msk'): WizardConfig => {
  const clusterData = getClusterDataBySourceType(sourceType, clusterKey)

  const topics = clusterData?.kafka_admin_client_information?.topics?.details || []
  const topicNames = topics.filter((topic: any) => !topic.name.startsWith('__')).map((topic: any) => topic.name)
  const hasTopics = topicNames.length > 0

  return {
    id: 'migrate-topics-migration-scripts-wizard',
    title: 'Migrate Topics Migration Scripts Wizard',
    description: 'Configure your migrate-topics Terraform — mirror existing topics with data forward, or scaffold plain Confluent Cloud topics for a greenfield migration.',
    apiEndpoint: '/assets/migration-scripts/topics',
    initial: 'mode_selection',

    states: {
      mode_selection: {
        meta: {
          title: 'Migration Mode',
          description:
            'Choose how to migrate topics. Mirror creates mirror topics that forward data from the source over an already established cluster link. New creates standard Confluent Cloud topics with exact configurations but no data forward — useful for greenfield migrations.',
          schema: {
            type: 'object',
            properties: {
              mode: {
                type: 'string',
                title: 'Mode',
                enum: [MODE_MIRROR, MODE_NEW],
                enumNames: ['mirror — cluster-link mirror topics (forwards data)', 'new — plain CC topics (no data forward)'],
                default: MODE_MIRROR,
              },
              // Hidden — the backend uses these to hydrate full TopicDetails
              // (partitions, configs) from state when mode=new.
              source_type: {
                type: 'string',
                default: sourceType,
              },
              cluster_id: {
                type: 'string',
                default: clusterKey,
              },
            },
            required: ['mode'],
          },
          uiSchema: {
            mode: {
              'ui:widget': 'radio',
            },
            source_type: {
              'ui:widget': 'hidden',
            },
            cluster_id: {
              'ui:widget': 'hidden',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'target_cluster_inputs_mirror',
              guard: 'is_mirror_mode',
              actions: 'save_step_data',
            },
            {
              target: 'target_cluster_inputs_new',
              guard: 'is_new_mode',
              actions: 'save_step_data',
            },
          ],
        },
      },

      target_cluster_inputs_mirror: {
        meta: {
          title: 'Migrate Topics | Target Cluster Inputs (mirror mode)',
          description:
            'Mirror mode requires an existing cluster link. The link must already be created — this wizard does not create it.',
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
                title: 'Cluster Link Name (existing link, not created here)',
              },
            },
            required: ['target_cluster_id', 'target_cluster_rest_endpoint', 'cluster_link_name'],
          },
          uiSchema: {
            target_cluster_id: { 'ui:placeholder': 'e.g., lkc-xxxxxx' },
            target_cluster_rest_endpoint: { 'ui:placeholder': 'e.g., https://xxx.xxx.aws.confluent.cloud:443' },
            cluster_link_name: { 'ui:placeholder': 'e.g., msk-to-cc-migration-link' },
          },
        },
        on: {
          NEXT: { target: 'topic_selection', actions: 'save_step_data' },
          BACK: { target: 'mode_selection', actions: 'undo_save_step_data' },
        },
      },

      target_cluster_inputs_new: {
        meta: {
          title: 'Migrate Topics | Target Cluster Inputs (new mode)',
          description:
            'New mode creates plain Confluent Cloud topics. No cluster link is required. Source topic partitions and CC-supported configs will be preserved; non-CC-supported configs and replication.factor are dropped.',
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
            },
            required: ['target_cluster_id', 'target_cluster_rest_endpoint'],
          },
          uiSchema: {
            target_cluster_id: { 'ui:placeholder': 'e.g., lkc-xxxxxx' },
            target_cluster_rest_endpoint: { 'ui:placeholder': 'e.g., https://xxx.xxx.aws.confluent.cloud:443' },
          },
        },
        on: {
          NEXT: { target: 'topic_selection', actions: 'save_step_data' },
          BACK: { target: 'mode_selection', actions: 'undo_save_step_data' },
        },
      },

      topic_selection: {
        meta: {
          title: 'Select Topics',
          description: `Select the topics to migrate from ${clusterData?.name}.`,
          schema: {
            type: 'object',
            properties: {
              selected_topics: {
                type: 'array',
                title: 'Topics',
                default: hasTopics ? topicNames : [],
                description: hasTopics
                  ? `Select one or more topics to migrate (${topicNames.length} topics available)`
                  : `No topics found in state for ${clusterData?.name}. Run \`kcp scan clusters\` to populate, then reload this wizard.`,
                items: {
                  type: 'string',
                  enum: topicNames,
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
              'ui:options': { enum: topicNames },
            },
          },
        },
        on: {
          NEXT: { target: 'confirmation', actions: 'save_step_data' },
        },
      },

      confirmation: {
        meta: {
          title: 'Review Configuration',
          description: 'Review your configuration before generating Terraform files',
        },
        on: {
          CONFIRM: { target: 'complete' },
          BACK: { target: 'mode_selection', actions: 'undo_save_step_data' },
        },
      },

      complete: {
        type: 'final',
        meta: {
          title: 'Configuration Complete',
          message: 'Your migrate-topics Terraform is ready to be processed...',
        },
      },
    },

    guards: {
      is_mirror_mode: ({ event }) => event.data?.mode === MODE_MIRROR,
      is_new_mode: ({ event }) => event.data?.mode === MODE_NEW,
      came_from_topic_selection: ({ context }) => context.previousStep === 'topic_selection',
    },

    actions: {
      save_step_data: 'save_step_data',
      undo_save_step_data: 'undo_save_step_data',
    },
  }
}
