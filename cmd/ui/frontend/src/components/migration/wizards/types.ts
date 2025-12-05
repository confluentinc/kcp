import type { RJSFSchema, UiSchema } from '@rjsf/utils'

// Types for the wizard system
export interface WizardStep {
  id: string
  title: string
  description?: string
  schema: RJSFSchema
  uiSchema?: UiSchema
  type?: 'form' | 'complete'
}

// Wizard form data can contain any JSON-serializable values
export type WizardFormData = Record<string, unknown>

export interface WizardEvent {
  type: 'NEXT' | 'BACK' | 'SUBMIT'
  data?: WizardFormData
  stepId?: string
}

export interface WizardContext {
  stepData: Record<string, WizardFormData>
  allData: Record<string, WizardFormData>
  currentStep: string
  previousStep?: string
  visitedSteps: string[]
}

export interface WizardConfig {
  id: string
  title: string
  description: string
  guards: Record<string, (params: { context: WizardContext; event: WizardEvent }) => boolean>
  actions: Record<string, unknown>
  apiEndpoint: string
  initial?: string
  states: Record<string, unknown>
  // Optional function to transform form data before sending to API
  transformPayload?: (data: Record<string, unknown>) => Record<string, unknown>
}

// Terraform module structure
export interface TerraformModule {
  name: string
  'main.tf'?: string
  'variables.tf'?: string
  'outputs.tf'?: string
  'versions.tf'?: string
  additional_files?: Record<string, string> | null

  // todo additional files for scripts flow
  'providers.tf'?: string
  'inputs.auto.tfvars'?: string
}

// Terraform files response structure from API
export interface TerraformFiles {
  'main.tf'?: string
  'providers.tf'?: string
  'variables.tf'?: string
  'outputs.tf'?: string
  'inputs.auto.tfvars'?: string
  modules?: TerraformModule[]
}

// Tree node structure for react-arborist
export interface TreeNode {
  id: string
  name: string
  children?: TreeNode[]
  content?: string // File content
  isFolder?: boolean
}

export interface WizardState {
  currentStep: string
  stepData: Record<string, WizardFormData>
  allData: Record<string, WizardFormData>
  isLoading: boolean
  terraformFiles: TerraformFiles | null
  error: string | null
}
