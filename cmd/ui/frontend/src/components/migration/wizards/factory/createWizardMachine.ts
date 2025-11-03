import { createMachine, assign } from 'xstate'
import type { WizardConfig, WizardContext, WizardEvent } from '../types'

export const createWizardMachine = (config: WizardConfig) => {
  const actions = {
    save_step_data: assign(({ context, event }: { context: WizardContext; event: WizardEvent }) => {
      const currentStateId = event?.stepId || context.currentStep || 'unknown'
      const eventData = event.data || {}
      const visitedSteps = context.visitedSteps || []

      // Always save/update form data for the current step
      const newStepData = { ...context.stepData }
      const newAllData = { ...context.allData }
      newStepData[currentStateId] = eventData
      newAllData[currentStateId] = eventData

      // Check if this step is already in visitedSteps
      const currentStepIndex = visitedSteps.indexOf(currentStateId)

      if (currentStepIndex !== -1) {
        // Step already in visitedSteps - we're re-visiting after going back
        // Remove all steps that come after this one from visitedSteps
        const stepsToRemove = visitedSteps.slice(currentStepIndex + 1)
        const updatedVisitedSteps = visitedSteps.slice(0, currentStepIndex + 1)

        // Delete data for steps on the old path
        stepsToRemove.forEach((stepId) => {
          delete newStepData[stepId]
          delete newAllData[stepId]
        })

        const result = {
          stepData: newStepData,
          allData: newAllData,
          currentStep: currentStateId,
          previousStep: currentStateId,
          visitedSteps: updatedVisitedSteps,
        }

        console.log(
          '➡️ NEXT:\n' +
            JSON.stringify(
              {
                from: currentStateId,
                visitedSteps: result.visitedSteps,
                stepData: result.stepData,
              },
              null,
              2
            )
        )

        return result
      }

      // New step - add it to visitedSteps
      const updatedVisitedSteps = [...visitedSteps, currentStateId]

      const result = {
        stepData: newStepData,
        allData: newAllData,
        currentStep: currentStateId,
        previousStep: currentStateId,
        visitedSteps: updatedVisitedSteps,
      }

      console.log(
        '➡️ NEXT:\n' +
          JSON.stringify(
            {
              from: currentStateId,
              visitedSteps: result.visitedSteps,
              stepData: result.stepData,
            },
            null,
            2
          )
      )

      return result
    }),
    // When going back, remove the CURRENT step from visitedSteps and delete its data
    undo_save_step_data: assign(
      ({ context, event }: { context: WizardContext; event: WizardEvent }) => {
        const targetStepId = event?.stepId || (event?.data?.targetStepId as string | undefined)
        const currentStepId =
          (event?.data?.currentStepId as string | undefined) || context.currentStep
        const visitedSteps = context.visitedSteps || []

        if (!targetStepId) {
          return context
        }

        // Remove the CURRENT step from visitedSteps and delete its data
        const updatedVisitedSteps = visitedSteps.filter((step) => step !== currentStepId)
        const newStepData = { ...context.stepData }
        const newAllData = { ...context.allData }
        delete newStepData[currentStepId]
        delete newAllData[currentStepId]

        const result = {
          ...context,
          stepData: newStepData,
          allData: newAllData,
          currentStep: targetStepId,
          previousStep: currentStepId,
          visitedSteps: updatedVisitedSteps,
        }

        console.log(
          '🔙 BACK:\n' +
            JSON.stringify(
              {
                from: currentStepId,
                to: targetStepId,
                visitedSteps: result.visitedSteps,
                stepData: result.stepData,
              },
              null,
              2
            )
        )

        return result
      }
    ),
  }

  // Build state machine configuration from config.states
  // Note: config.states is dynamically typed, so we use type assertion here
  // The states structure matches XState's expected format
  const initialStep = config.initial || Object.keys(config.states)[0]
  const machineConfig = {
    id: config.id,
    initial: initialStep,
    context: {
      stepData: {},
      allData: {},
      currentStep: initialStep,
      visitedSteps: [], // Start with empty - steps are added when you click Next FROM them
    } as WizardContext,
    states: config.states,
  }

  // XState v5 types are complex for dynamic configurations
  // Using type assertion here since we're building the machine dynamically from config
  return createMachine(machineConfig as Parameters<typeof createMachine>[0], {
    guards: config.guards as never,
    actions: actions as never,
  })
}
