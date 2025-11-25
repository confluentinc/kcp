import type { WizardConfig } from './types'
import { getClusterDataByArn } from '@/stores/store'
import type { Topic } from '@/types/aws/msk'

export const createTargetInfraWizardConfig = (clusterArn: string): WizardConfig => {
  const cluster = getClusterDataByArn(clusterArn)

  const topics = cluster?.kafka_admin_client_information?.topics?.details || []
  const topicNames = topics.filter((topic: Topic) => !topic.name.startsWith('__')).map((topic: Topic) => topic.name)

  return {
    id: 'mirror-topics-migration-scripts-wizard',
    title: 'Mirror Topics Migration Scripts Wizard',
    description: 'Configure your mirror topics migration scripts',
    apiEndpoint: '/assets/migration-scripts/topics',
    initial: 'mirror_topics_question',

    states: {
      target_cluster_inputs: {
        meta: {
          title: 'Mirror Topics | Target Cluster Inputs',
          schema: {
            type: 'object',
            properties: {
              target_environment_id: {
                type: 'boolean',
                title: 'I need to create a new Confluent Cloud environment',
                default: true,
              },
            },
            required: ['needs_environment'],
          },
          uiSchema: {
            needs_environment: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'create_environment',
              guard: 'needs_environment',
              actions: 'save_step_data',
            },
            {
              target: 'cluster_question',
              guard: 'does_not_need_environment',
              actions: 'save_step_data',
            },
            {
              target: 'cluster_question',
              guard: 'needs_environment',
              actions: 'save_step_data',
            },
          ],
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
              target: 'create_private_link',
              guard: 'came_from_create_private_link',
              actions: 'undo_save_step_data',
            },
            {
              target: 'private_link_question',
              guard: 'came_from_private_link_question',
              actions: 'undo_save_step_data',
            },
            {
              target: 'cluster_question',
              actions: 'undo_save_step_data',
            },
          ],
        },
      },
      complete: {
        type: 'final',
        meta: {
          title: 'Configuration Complete',
          message: 'Your Confluent Cloud configuration is ready to be processed...',
        },
      },
    },

    guards: {
      needs_environment: ({ event }) => {
        return event.data?.needs_environment === true
      },
      came_from_private_link_question: ({ context }) => {
        return context.previousStep === 'private_link_question'
      },
    },

    actions: {
      save_step_data: 'save_step_data',
      undo_save_step_data: 'undo_save_step_data',
    },
  }
}
