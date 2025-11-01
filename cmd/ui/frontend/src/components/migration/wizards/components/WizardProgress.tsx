interface WizardProgressProps {
  currentStepNumber: number
}

export function WizardProgress({ currentStepNumber }: WizardProgressProps) {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="text-sm text-gray-500 dark:text-gray-400">
          Step {currentStepNumber}
        </div>
      </div>

      <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-2">
        <div
          className="bg-blue-600 dark:bg-accent h-2 rounded-full transition-all duration-300 animate-pulse"
          style={{ width: '100%' }}
        />
      </div>
    </div>
  )
}
