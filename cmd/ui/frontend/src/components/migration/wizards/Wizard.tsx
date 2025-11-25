import { useMemo } from 'react'
import { useMachine } from '@xstate/react'
import { useAppStore } from '@/stores/store'
import { WizardStepForm } from './components/WizardStepForm'
import { WizardProgress } from './components/WizardProgress'
import { WizardConfirmation } from './components/WizardConfirmation'
import { useWizardAPI } from './hooks/useWizardAPI'
import { useWizardData } from './hooks/useWizardData'
import { createWizardMachine } from './factory/createWizardMachine'
import type { WizardConfig, WizardContext, WizardStep } from './types'
import type { WizardType } from '@/types'

interface WizardProps {
  config: WizardConfig
  clusterKey?: string
  wizardType?: WizardType
  onComplete?: () => void
  onClose?: () => void
}

export const Wizard = ({ config, clusterKey, wizardType, onComplete, onClose }: WizardProps) => {
  // Create the wizard machine
  const wizardMachine = useMemo(() => createWizardMachine(config), [config])
  const [state, send] = useMachine(wizardMachine)

  // API integration
  const { isLoading, generateTerraform } = useWizardAPI(config.apiEndpoint)

  // Data management
  const { flattenedData } = useWizardData(state.context as WizardContext)

  // Zustand store
  const setTerraformFiles = useAppStore((state) => state.setTerraformFiles)

  const currentStateId = state.value as string
  const currentStep = (config.states[currentStateId] as { meta?: unknown })?.meta
  
  // Check if we're on the initial/first step
  const isFirstStep = currentStateId === config.initial

  // Get context for back navigation
  const context = state.context as WizardContext

  const handleFormSubmit = async (formData: Record<string, unknown>) => {
    console.log('formData - ', JSON.stringify(formData, null, 2))
    // Send the event with form data
    send({
      type: 'NEXT',
      data: formData,
      stepId: currentStateId,
    })
  }

  const handleBack = () => {
    const visitedSteps = context.visitedSteps || []

    // Always use the last visited step as the back target
    let backTarget: string | undefined

    if (visitedSteps.length > 0) {
      backTarget = visitedSteps[visitedSteps.length - 1]
    } else {
      // Fallback: Try to get configured BACK target
      const currentStateConfig = config.states[currentStateId] as
        | { on?: { BACK?: { target?: string } | Array<{ target?: string }> } }
        | undefined

      const backConfig = currentStateConfig?.on?.BACK
      if (Array.isArray(backConfig)) {
        backTarget = backConfig[backConfig.length - 1]?.target
      } else if (backConfig && typeof backConfig === 'object') {
        backTarget = backConfig.target
      }
    }

    if (!backTarget) {
      console.error(`⚠️ No back target found for step '${currentStateId}'`)
      return
    }

    send({
      type: 'BACK',
      stepId: backTarget,
      data: {
        targetStepId: backTarget,
        currentStepId: currentStateId,
      },
    })
  }

  const handleConfirmation = async () => {
    try {
      const files = await generateTerraform(flattenedData)

      // Store files in zustand if cluster info is provided
      if (clusterKey && wizardType && files) {
        setTerraformFiles(clusterKey, wizardType, files)
      }

      // Call onComplete callback to exit wizard and switch tab
      if (onComplete) {
        console.log('onComplete - ')
        onComplete()
      }
    } catch {
      // Failed to generate terraform - error is already logged by useWizardAPI
    }
  }

  // Handle confirmation state
  if (currentStateId === 'confirmation') {
    return (
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        <WizardProgress />
        <WizardConfirmation
          data={flattenedData}
          onConfirm={handleConfirmation}
          onBack={handleBack}
          isLoading={isLoading}
        />
      </div>
    )
  }

  // Handle regular form steps
  if (!currentStep) {
    // Invalid step configuration - this should not happen in normal operation
    return <div className="text-gray-900 dark:text-gray-100">Invalid step configuration</div>
  }

  // Type guard to ensure currentStep is a WizardStep
  const stepData = (state.context.stepData?.[currentStateId] as Record<string, unknown>) || {}

  return (
    <div className="max-w-2xl mx-auto p-6 space-y-6">
      <WizardProgress />

      <WizardStepForm
        step={currentStep as WizardStep}
        formData={stepData}
        onSubmit={handleFormSubmit}
        onBack={handleBack}
        onClose={isFirstStep ? onClose : undefined}
        canGoBack={!isFirstStep}
        isLoading={isLoading}
      />
    </div>
  )
}
