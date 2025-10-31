import { createMachine, assign } from 'xstate'
import type { WizardConfig, WizardContext, WizardEvent } from '../types'

export function createWizardMachine(config: WizardConfig) {
  const actions = {
    save_step_data: assign(({ context, event }: { context: WizardContext; event: WizardEvent }) => {
      const currentStateId = event?.stepId || context.currentStep || 'unknown'
      const eventData = event.data || {}

      return {
        stepData: {
          ...context.stepData,
          [currentStateId]: eventData,
        },
        allData: {
          ...context.allData,
          [currentStateId]: eventData,
        },
        previousStep: currentStateId,
      }
    }),
  }

  // Build state machine configuration from config.states
  // Note: config.states is dynamically typed, so we use type assertion here
  // The states structure matches XState's expected format
  const machineConfig = {
    id: config.id,
    initial: config.initial || Object.keys(config.states)[0],
    context: {
      stepData: {},
      allData: {},
      currentStep: config.initial || Object.keys(config.states)[0],
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
