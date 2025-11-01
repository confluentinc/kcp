interface WizardProgressProps {
  currentStepNumber: number
  totalSteps: number
}

export function WizardProgress({ currentStepNumber, totalSteps }: WizardProgressProps) {
  // Calculate progress percentage (0-100)
  // Show progress based on current step position (so step 1 shows some progress, not 0%)
  const progress = totalSteps > 0 ? (currentStepNumber / totalSteps) * 100 : 0

  return (
    <div className="w-full bg-gray-200 dark:bg-gray-700 rounded-full h-2">
      <div
        className="bg-blue-600 dark:bg-accent h-2 rounded-full transition-all duration-300"
        style={{ width: `${Math.min(Math.max(progress, 0), 100)}%` }}
      />
    </div>
  )
}
