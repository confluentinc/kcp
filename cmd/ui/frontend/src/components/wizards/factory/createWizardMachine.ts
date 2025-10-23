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

  return createMachine(machineConfig as any, {
    guards: config.guards as any,
    actions: actions as any,
  })
}
