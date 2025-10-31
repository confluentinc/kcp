import { useMemo } from 'react'
import type { WizardContext, WizardFormData } from '../types'

export function useWizardData(context: WizardContext) {
  const flattenedData = useMemo(() => {
    const flattened: Record<string, unknown> = {}

    // Try allData first
    Object.entries(context.allData || {}).forEach(([, stepData]: [string, WizardFormData]) => {
      if (stepData && typeof stepData === 'object') {
        Object.assign(flattened, stepData)
      }
    })

    // Fallback to stepData if allData is empty
    if (Object.keys(flattened).length === 0) {
      Object.entries(context.stepData || {}).forEach(([, stepData]: [string, WizardFormData]) => {
        if (stepData && typeof stepData === 'object') {
          Object.assign(flattened, stepData)
        }
      })
    }

    return flattened
  }, [context.allData, context.stepData])

  return {
    flattenedData,
  }
}
