/**
 * Types derived from application constants
 */

import {
  TAB_IDS,
  TOP_LEVEL_TABS,
  COST_TYPES,
  CLUSTER_REPORT_TABS,
  CONNECTOR_TABS,
  WIZARD_TYPES,
} from '@/constants'

/**
 * Type representing valid tab IDs for metrics and costs views
 */
export type TabId = (typeof TAB_IDS)[keyof typeof TAB_IDS]

/**
 * Type representing valid top-level application tabs
 */
export type TopLevelTab = (typeof TOP_LEVEL_TABS)[keyof typeof TOP_LEVEL_TABS]

/**
 * Type representing valid cost types
 */
export type CostType = (typeof COST_TYPES)[keyof typeof COST_TYPES]

/**
 * Type representing valid cluster report tab IDs
 */
export type ClusterReportTab = (typeof CLUSTER_REPORT_TABS)[keyof typeof CLUSTER_REPORT_TABS]

/**
 * Type representing valid connector tab IDs
 */
export type ConnectorTab = (typeof CONNECTOR_TABS)[keyof typeof CONNECTOR_TABS]

/**
 * Type representing valid wizard types
 */
export type WizardType = (typeof WIZARD_TYPES)[keyof typeof WIZARD_TYPES]
