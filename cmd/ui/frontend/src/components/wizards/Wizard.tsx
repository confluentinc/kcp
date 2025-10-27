import { useMemo } from 'react'
import { useMachine } from '@xstate/react'
import { WizardStepForm } from './components/WizardStepForm'
import { WizardProgress } from './components/WizardProgress'
import { WizardComplete } from './components/WizardComplete'
import { WizardConfirmation } from './components/WizardConfirmation'
import { useWizardAPI } from './hooks/useWizardAPI'
import { useWizardData } from './hooks/useWizardData'
import { createWizardMachine } from './factory/createWizardMachine'
import type { WizardConfig } from './types'

interface WizardProps {
  config: WizardConfig
}

export function Wizard({ config }: WizardProps) {
  // Create the wizard machine
  const wizardMachine = useMemo(() => createWizardMachine(config), [config])
  const [state, send] = useMachine(wizardMachine)

  // API integration
  const { isLoading, terraformFiles, error, generateTerraform } = useWizardAPI(config.apiEndpoint)

  // Data management
  const { flattenedData } = useWizardData(state.context as any)

  const currentStateId = state.value as string
  const currentStep = config.states[currentStateId]?.meta

  // Calculate progress
  const allSteps = Object.keys(config.states).filter((key) => config.states[key].type !== 'final')
  const currentIndex = allSteps.indexOf(currentStateId)
  const totalSteps = allSteps.length

  const handleFormSubmit = async (formData: any) => {
    // Send the event with form data
    send({
      type: 'NEXT',
      data: formData,
      stepId: currentStateId,
    })
  }

  const handleBack = () => {
    send({ type: 'BACK' })
  }

  const handleConfirmation = async () => {
    try {
      await generateTerraform(flattenedData)
      // Transition to complete state after generation
      send({ type: 'CONFIRM' })
    } catch (err) {
      console.error('Failed to generate terraform:', err)
    }
  }

  const handleRegenerate = async () => {
    try {
      await generateTerraform(flattenedData)
    } catch (err) {
      console.error('Failed to regenerate terraform:', err)
    }
  }

  // Handle confirmation state
  if (currentStateId === 'confirmation') {
    return (
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        <WizardProgress
          currentIndex={currentIndex}
          totalSteps={totalSteps}
        />
        <WizardConfirmation
          data={flattenedData}
          onConfirm={handleConfirmation}
          onBack={handleBack}
          isLoading={isLoading}
        />
      </div>
    )
  }

  // Handle complete state or when terraform files are ready
  if (state.matches('complete') || terraformFiles || error) {
    return (
      <div className="max-w-4xl mx-auto p-6">
        <WizardComplete
          terraformFiles={terraformFiles}
          isLoading={isLoading}
          error={error}
          onRegenerate={handleRegenerate}
        />
      </div>
    )
  }

  // Handle regular form steps
  if (!currentStep) {
    console.log('Invalid step configuration from Wizard.tsx', currentStep)
    return <div className="text-gray-900 dark:text-gray-100">Invalid step configuration</div>
  }

  return (
    <div className="max-w-2xl mx-auto p-6 space-y-6">
      <WizardProgress
        currentIndex={currentIndex}
        totalSteps={totalSteps}
      />

      <WizardStepForm
        step={currentStep}
        formData={(state.context.stepData as Record<string, any>)?.[currentStateId] || {}}
        onSubmit={handleFormSubmit}
        onBack={handleBack}
        canGoBack={currentIndex > 0}
        isLoading={isLoading}
      />
    </div>
  )
}
