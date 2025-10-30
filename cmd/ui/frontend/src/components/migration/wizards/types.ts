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
}

export interface TerraformFiles {
  main_tf?: string
  providers_tf?: string
  variables_tf?: string
  [key: string]: string | undefined
}

export interface WizardState {
  currentStep: string
  stepData: Record<string, WizardFormData>
  allData: Record<string, WizardFormData>
  isLoading: boolean
  terraformFiles: TerraformFiles | null
  error: string | null
}
