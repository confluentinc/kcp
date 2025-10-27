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

export interface WizardEvent {
  type: 'NEXT' | 'BACK' | 'SUBMIT'
  data?: Record<string, any>
  stepId?: string
}

export interface WizardContext {
  stepData: Record<string, any>
  allData: Record<string, any>
  currentStep: string
  previousStep?: string
}

export interface WizardConfig {
  id: string
  title: string
  description: string
  guards: Record<string, (params: { context: WizardContext; event: WizardEvent }) => boolean>
  actions: Record<string, any>
  apiEndpoint: string
  initial?: string
  states: Record<string, any>
}

export interface TerraformFiles {
  main_tf?: string
  providers_tf?: string
  variables_tf?: string
  [key: string]: string | undefined
}

export interface WizardState {
  currentStep: string
  stepData: Record<string, any>
  allData: Record<string, any>
  isLoading: boolean
  terraformFiles: TerraformFiles | null
  error: string | null
}
