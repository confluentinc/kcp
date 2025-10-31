import { Button } from '@/components/common/ui/button'
import type { WizardFormData } from '../types'

interface WizardConfirmationProps {
  data: WizardFormData
  onConfirm: () => void
  onBack: () => void
  isLoading: boolean
}

export function WizardConfirmation({
  data,
  onConfirm,
  onBack,
  isLoading,
}: WizardConfirmationProps) {
  const renderValue = (value: unknown): string => {
    if (value === null || value === undefined) return 'N/A'
    if (typeof value === 'boolean') return value ? 'Yes' : 'No'
    if (typeof value === 'object') return JSON.stringify(value, null, 2)
    return String(value)
  }

  const formatKey = (key: string): string => {
    return key
      .split('_')
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
      .join(' ')
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">
          Review Your Configuration
        </h2>
        <p className="text-gray-600 dark:text-gray-400 mt-2">
          Please review all your choices before generating the Terraform files.
        </p>
      </div>

      <div className="border border-gray-200 dark:border-border rounded-lg p-6 bg-gray-50 dark:bg-card">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-2">
          Configuration Summary
        </h3>
        <p className="text-gray-600 dark:text-gray-400 text-sm mb-4">
          All the choices you made during the wizard
        </p>
        <div className="space-y-4">
          {Object.entries(data).map(([key, value]) => (
            <div
              key={key}
              className="border-b border-gray-200 dark:border-border pb-4 last:border-0"
            >
              <div className="font-semibold text-gray-900 dark:text-gray-100 mb-1">
                {formatKey(key)}
              </div>
              <div className="text-gray-600 dark:text-gray-400 text-sm font-mono">
                {renderValue(value)}
              </div>
            </div>
          ))}
        </div>
      </div>

      <div className="flex gap-4">
        <Button
          type="button"
          onClick={onBack}
          variant="outline"
          disabled={isLoading}
          className="flex-1"
        >
          Back
        </Button>
        <Button
          type="button"
          onClick={onConfirm}
          disabled={isLoading}
          className="flex-1"
        >
          {isLoading ? 'Generating...' : 'Generate Terraform Files'}
        </Button>
      </div>
    </div>
  )
}
