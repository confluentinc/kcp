import type { WizardContext } from '../types'

interface WizardDebugPanelProps {
  context: WizardContext
  currentStepId: string
}

export function WizardDebugPanel({ context, currentStepId }: WizardDebugPanelProps) {
  const visitedSteps = context.visitedSteps || []
  const currentStepIndex = visitedSteps.indexOf(currentStepId)
  const stepDataKeys = Object.keys(context.stepData || {})
  const allDataKeys = Object.keys(context.allData || {})

  return (
    <div className="mt-6 border border-yellow-400 dark:border-yellow-600 rounded-lg p-4 bg-yellow-50 dark:bg-yellow-900/20">
      <h3 className="text-sm font-bold text-yellow-900 dark:text-yellow-200 mb-3">🐛 Debug Info</h3>
      <div className="space-y-3 text-xs font-mono">
        <div>
          <span className="font-semibold text-yellow-800 dark:text-yellow-300">
            Current Step (state.value):
          </span>
          <span className="ml-2 text-yellow-700 dark:text-yellow-400 font-bold">
            {currentStepId}
          </span>
        </div>
        <div>
          <span className="font-semibold text-yellow-800 dark:text-yellow-300">
            Context Current Step:
          </span>
          <span className="ml-2 text-yellow-700 dark:text-yellow-400">
            {context.currentStep || 'N/A'}
          </span>
        </div>
        <div>
          <span className="font-semibold text-yellow-800 dark:text-yellow-300">Previous Step:</span>
          <span className="ml-2 text-yellow-700 dark:text-yellow-400">
            {context.previousStep || 'N/A'}
          </span>
        </div>
        <div>
          <span className="font-semibold text-yellow-800 dark:text-yellow-300">
            Visited Steps ({visitedSteps.length}):
          </span>
          <div className="ml-2 mt-1 text-yellow-700 dark:text-yellow-400">
            {visitedSteps.length > 0 ? (
              <span>
                [
                {visitedSteps.map((step, idx) => (
                  <span key={step}>
                    {idx > 0 && ', '}
                    <span className={step === currentStepId ? 'font-bold underline' : ''}>
                      {step}
                    </span>
                    {step === currentStepId && ` (index: ${currentStepIndex})`}
                  </span>
                ))}
                ]
              </span>
            ) : (
              '[]'
            )}
          </div>
        </div>
        <div>
          <span className="font-semibold text-yellow-800 dark:text-yellow-300">
            Step Data ({stepDataKeys.length} steps):
          </span>
          <pre className="ml-2 mt-1 p-2 bg-yellow-100 dark:bg-yellow-900/40 rounded overflow-auto max-h-40 text-yellow-800 dark:text-yellow-300">
            {JSON.stringify(context.stepData || {}, null, 2)}
          </pre>
        </div>
        <div>
          <span className="font-semibold text-yellow-800 dark:text-yellow-300">
            All Data ({allDataKeys.length} steps):
          </span>
          <pre className="ml-2 mt-1 p-2 bg-yellow-100 dark:bg-yellow-900/40 rounded overflow-auto max-h-40 text-yellow-800 dark:text-yellow-300">
            {JSON.stringify(context.allData || {}, null, 2)}
          </pre>
        </div>
      </div>
    </div>
  )
}
