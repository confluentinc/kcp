/**
 * Shared form schema fragments for migration infrastructure wizards.
 * Used by both MSK and OSK wizard configs.
 */

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
    title: 'Confluent Cloud Cluster Link Name',
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
