/**
 * Shared form schema fragments for migration infrastructure wizards.
 * Used by both MSK and OSK wizard configs.
 */

import type { WizardContext, WizardEvent } from './types'

/**
 * Confluent Cloud destination declaration. Gates linking-based wizard paths
 * that are unsupported on Confluent Cloud for Government (Cluster Linking,
 * Schema Linking). The string values match the CLI's --cc-environment values
 * and are sent to the backend unchanged.
 */
export const DESTINATION_FIELD = 'cc_environment'
export const DESTINATION_CC = 'cc'
export const DESTINATION_CC_GOV = 'cc-gov'

/** Exact product name — rendered verbatim wherever Gov is referenced. */
export const CC_GOV_PRODUCT_NAME = 'Confluent Cloud for Government'

/**
 * Shared destination-type question step meta (radio: Standard | Gov).
 * Standard → 'cc', Gov → 'cc-gov'. Spread into a wizard's initial state so the
 * same question and field name are reused everywhere.
 */
export const destinationTypeStepMeta = () => ({
  title: 'Confluent Cloud Destination',
  description: 'What is your Confluent Cloud destination type? Standard or Gov.',
  schema: {
    type: 'object' as const,
    properties: {
      cc_environment: {
        type: 'string' as const,
        title: 'Confluent Cloud destination type',
        oneOf: [
          { title: 'Standard', const: DESTINATION_CC },
          { title: `Gov (${CC_GOV_PRODUCT_NAME})`, const: DESTINATION_CC_GOV },
        ],
      },
    },
    required: ['cc_environment'],
  },
  uiSchema: {
    cc_environment: {
      'ui:widget': 'radio',
    },
  },
})

/**
 * Shared terminal "blocked" step meta for Gov-unsupported paths. The message
 * (R13) must name the linking dependency and the supported alternative where
 * one exists, and use the exact product name (R14). Rendered by Wizard.tsx's
 * gov_unsupported branch — no form, no path to generation.
 */
export const govUnsupportedStepMeta = (message: string) => ({
  title: `Unsupported on ${CC_GOV_PRODUCT_NAME}`,
  description: message,
})

/**
 * Reusable destination guards reading the just-submitted declaration from the
 * destination-type step's NEXT event. Spread into a wizard's guards map.
 */
export const destinationGuards = {
  is_gov: ({ event }: { context: WizardContext; event: WizardEvent }) =>
    event.data?.[DESTINATION_FIELD] === DESTINATION_CC_GOV,
  is_standard: ({ event }: { context: WizardContext; event: WizardEvent }) =>
    event.data?.[DESTINATION_FIELD] === DESTINATION_CC,
}

/** Target cluster properties for public cluster link path */
export const targetClusterProperties = () => ({
  target_cluster_id: {
    type: 'string' as const,
    title: 'Confluent Cloud Cluster ID',
  },
  target_rest_endpoint: {
    type: 'string' as const,
    title: 'Confluent Cloud Cluster REST Endpoint',
  },
  cluster_link_name: {
    type: 'string' as const,
    title: 'Cluster Link Name (created during migration)',
  },
})

export const targetClusterUiSchema = () => ({
  target_cluster_id: {
    'ui:placeholder': 'e.g., lkc-xxxxxx',
  },
  target_rest_endpoint: {
    'ui:placeholder': 'e.g., https://xxx.xxx.aws.confluent.cloud:443',
  },
  cluster_link_name: {
    'ui:placeholder': 'e.g., source-to-cc-migration-link',
  },
})

/** Additional target properties for jump cluster and external outbound paths */
export const jumpClusterTargetProperties = () => ({
  target_environment_id: {
    type: 'string' as const,
    title: 'Confluent Cloud Environment ID',
  },
  target_bootstrap_endpoint: {
    type: 'string' as const,
    title: 'Confluent Cloud Cluster Bootstrap Endpoint',
  },
  existing_private_link_vpce_id: {
    type: 'string' as const,
    title: 'Existing PrivateLink VPC Endpoint ID',
  },
})

export const jumpClusterTargetUiSchema = () => ({
  target_environment_id: {
    'ui:placeholder': 'e.g., env-xxxxxx',
  },
  target_bootstrap_endpoint: {
    'ui:placeholder': 'e.g., xxx.xxx.aws.confluent.cloud:9092',
  },
  existing_private_link_vpce_id: {
    'ui:placeholder': 'e.g., vpce-xxxxxx',
  },
})
