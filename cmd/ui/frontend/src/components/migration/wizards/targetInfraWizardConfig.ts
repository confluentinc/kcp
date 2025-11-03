import type { WizardConfig } from './types'
import { getClusterDataByArn } from '@/stores/store'

export const createTargetInfraWizardConfig = (clusterArn: string): WizardConfig => {
  const cluster = getClusterDataByArn(clusterArn)
  console.log('Target Infra Wizard - Cluster:', cluster)

  return {
    id: 'target-infra-wizard',
    title: 'Target Infrastructure Wizard',
    description: 'Configure your target infrastructure for migration',
    apiEndpoint: '/assets/target',
    initial: 'environment_question',

    states: {
      environment_question: {
        meta: {
          title: 'Environment Configuration',
          schema: {
            type: 'object',
            properties: {
              needs_environment: {
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
      create_environment: {
        meta: {
          title: 'Create Confluent Cloud Environment and Cluster',
          description: 'Enter details for your new Confluent Cloud environment and cluster',
          schema: {
            type: 'object',
            properties: {
              environment_name: {
                type: 'string',
                title: 'Environment Name',
                description: 'Name for your new Confluent Cloud environment',
              },
              cluster_name: {
                type: 'string',
                title: 'Cluster Name',
                default: cluster?.name,
                description: 'Name for your new Confluent Cloud cluster',
              },
              cluster_type: {
                type: 'string',
                enum: ['dedicated', 'enterprise'],
                title: 'Cluster Type',
                description: 'Select the type of cluster',
              },
            },
            required: ['environment_name', 'cluster_name', 'cluster_type'],
          },
          uiSchema: {
            environment_name: {
              'ui:placeholder': 'e.g., production-env',
            },
            cluster_name: {
              'ui:placeholder': 'e.g., production-cluster',
            },
            cluster_type: {
              'ui:widget': 'select',
            },
          },
        },
        on: {
          NEXT: {
            target: 'private_link_question',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'environment_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      cluster_question: {
        meta: {
          title: 'Cluster Configuration',
          description: 'Do you need to create a new cluster in your existing environment?',
          schema: {
            type: 'object',
            properties: {
              needs_cluster: {
                type: 'boolean',
                title: 'I need to create a new cluster',
                default: false,
              },
            },
            required: ['needs_cluster'],
          },
          uiSchema: {
            needs_cluster: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'create_cluster',
              guard: 'needs_cluster',
              actions: 'save_step_data',
            },
            {
              target: 'confirmation',
              guard: 'does_not_need_cluster',
              actions: 'save_step_data',
            },
          ],
          BACK: {
            target: 'environment_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      create_cluster: {
        meta: {
          title: 'Create Confluent Cloud Cluster',
          description: 'Enter details for your new Confluent Cloud cluster',
          schema: {
            type: 'object',
            properties: {
              environment_id: {
                type: 'string',
                title: 'Environment ID',
                description: 'ID of the Confluent Cloud environment',
              },
              cluster_name: {
                type: 'string',
                title: 'Cluster Name',
                description: 'Name for your new Confluent Cloud cluster',
              },
              cluster_type: {
                type: 'string',
                enum: ['dedicated', 'enterprise'],
                title: 'Cluster Type',
                description: 'Select the type of cluster',
              },
            },
            required: ['environment_id', 'cluster_name', 'cluster_type'],
          },
          uiSchema: {
            environment_id: {
              'ui:placeholder': 'e.g., env-xxxx',
            },
            cluster_name: {
              'ui:placeholder': 'e.g., production-cluster',
            },
            cluster_type: {
              'ui:widget': 'select',
            },
          },
        },
        on: {
          NEXT: {
            target: 'private_link_question',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'cluster_question',
            actions: 'undo_save_step_data',
          },
        },
      },
      private_link_question: {
        meta: {
          title: 'Setup private linking',
          schema: {
            type: 'object',
            properties: {
              needs_private_link: {
                type: 'boolean',
                title: 'Setup private linking for your Confluent Cloud cluster',
                default: true,
              },
            },
            required: ['needs_private_link'],
          },
          uiSchema: {
            needs_private_link: {
              'ui:widget': 'radio',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'create_private_link',
              guard: 'needs_private_link',
              actions: 'save_step_data',
            },
            {
              target: 'confirmation',
              guard: 'does_not_need_private_link',
              actions: 'save_step_data',
            },
          ],
          BACK: [
            {
              target: 'create_environment',
              guard: 'came_from_create_environment',
              actions: 'undo_save_step_data',
            },
            {
              target: 'create_cluster',
              guard: 'came_from_create_cluster',
              actions: 'undo_save_step_data',
            },
            {
              target: 'cluster_question',
              actions: 'undo_save_step_data',
            },
          ],
        },
      },
      create_private_link: {
        meta: {
          title: 'Private Link Configuration',
          description: 'Enter details for your private link configuration',
          schema: {
            type: 'object',
            properties: {
              vpc_id: {
                type: 'string',
                title: 'VPC ID',
              },
              subnet_cidr_ranges: {
                type: 'array',
                title: 'Private link new subnets CIDR ranges',
                items: {
                  type: 'string',
                },
                minItems: 3,
                maxItems: 3,
                default: ['', '', ''],
              },
            },
            required: ['vpc_id', 'subnet_cidr_ranges'],
          },
          uiSchema: {
            vpc_id: {
              'ui:placeholder': 'e.g., vpc-xxxx',
            },
            subnet_cidr_ranges: {
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
            target: 'confirmation',
            actions: 'save_step_data',
          },
          BACK: {
            target: 'private_link_question',
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
      does_not_need_environment: ({ event }) => {
        return event.data?.needs_environment === false
      },
      needs_cluster: ({ event }) => {
        return event.data?.needs_cluster === true
      },
      does_not_need_cluster: ({ event }) => {
        return event.data?.needs_cluster === false
      },
      does_not_need_private_link: ({ event }) => {
        return event.data?.needs_private_link === false
      },
      needs_private_link: ({ event }) => {
        return event.data?.needs_private_link === true
      },
      came_from_create_environment: ({ context }) => {
        return context.previousStep === 'create_environment'
      },
      came_from_create_cluster: ({ context }) => {
        return context.previousStep === 'create_cluster'
      },
      came_from_cluster_question: ({ context }) => {
        return context.previousStep === 'cluster_question'
      },
      came_from_create_private_link: ({ context }) => {
        return context.previousStep === 'create_private_link'
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
