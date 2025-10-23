interface WizardProgressProps {
  currentIndex: number
  totalSteps: number
}

export function WizardProgress({ currentIndex, totalSteps }: WizardProgressProps) {
  const progress = totalSteps > 0 ? ((currentIndex + 1) / totalSteps) * 100 : 0

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="text-sm text-gray-500 dark:text-gray-400">
          Step {currentIndex + 1} of {totalSteps}
        </div>
      </div>

      <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-2">
        <div
          className="bg-blue-600 dark:bg-blue-500 h-2 rounded-full transition-all duration-300"
          style={{ width: `${progress}%` }}
        />
      </div>
    </div>
  )
}
