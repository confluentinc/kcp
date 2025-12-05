import type { WizardConfig } from './types'
import { getClusterDataByArn } from '@/stores/store'

export const createAclMigrationScriptsWizardConfig = (clusterArn: string): WizardConfig => {
  const cluster = getClusterDataByArn(clusterArn)

  const principals = cluster?.kafka_admin_client_information?.acls?.map((acl: any) => acl.Principal) || []
  const principalsFiltered = principals.filter((principal: any) => principal !== null && principal !== undefined && principal !== '')
  const principalEnumValues = principalsFiltered.filter((principal, index) => 
    principalsFiltered.indexOf(principal) === index
  )

  console.log(principalEnumValues)

  return {
    id: 'acl-migration-scripts-wizard',
    title: 'ACL Migration Scripts Wizard',
    description: 'Configure your ACL migration scripts',
    apiEndpoint: '/assets/migration-scripts/acls',
    initial: 'target_cluster_inputs',

    states: {
      target_cluster_inputs: {
        meta: {
          title: 'ACL Migration | Target Cluster Inputs',
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
            target_cluster_id: {
              'ui:placeholder': 'e.g., lkc-xxxxxx',
            },
            target_cluster_rest_endpoint: {
              'ui:placeholder': 'e.g., https://xxx.xxx.aws.confluent.cloud:443',
            },
          },
        },
        on: {
          NEXT: [
            {
              target: 'acl_principal_selection',
              actions: 'save_step_data',
            },
          ],
        },
      },
      acl_principal_selection: {
        meta: {
          title: 'ACL Migration | Select Principals',
          description: `Select the principals you wish to migrate along with their ACLs to Confluent Cloud from ${cluster?.name}.`,
          schema: {
            type: 'object',
            properties: {
              selected_principals: {
                type: 'array',
                title: 'Principals',
                default: principalEnumValues,
                description: `Select one or more principals to migrate (${principalEnumValues.length} principals available)`,
                items: {
                  type: 'string',
                  enum: principalEnumValues,
                },
                uniqueItems: true,
                minItems: 1,
              },
            },
            required: ['selected_principals'],
          },
          uiSchema: {
            selected_principals: {
              'ui:widget': 'checkboxes',
              'ui:options': {
                enum: principalEnumValues,
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
          BACK:
          {
            target: 'acl_principal_selection',
            actions: 'undo_save_step_data',
          },
        },
      },
      complete: {
        type: 'final',
        meta: {
          title: 'Configuration Complete',
          message: 'Your principal ACL migration scripts are ready to be processed...',
        },
      },
    },

    guards: {},

    actions: {
      save_step_data: 'save_step_data',
      undo_save_step_data: 'undo_save_step_data',
    },
  }
}
