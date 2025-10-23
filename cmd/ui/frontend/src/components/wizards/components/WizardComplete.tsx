import { Button } from '@/components/ui/button'
import { TerraformDisplay } from './TerraformDisplay'
import type { TerraformFiles } from '../types'

interface WizardCompleteProps {
  terraformFiles: TerraformFiles | null
  isLoading: boolean
  error: string | null
  onRegenerate: () => void
}

export function WizardComplete({
  terraformFiles,
  isLoading,
  error,
  onRegenerate,
}: WizardCompleteProps) {
  if (error) {
    return (
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        <div className="text-center">
          <h2 className="text-2xl font-bold text-red-600 dark:text-red-400 mb-4">
            Error Generating Terraform Files
          </h2>
          <p className="text-gray-600 dark:text-gray-400 mb-6">{error}</p>
          <Button
            onClick={onRegenerate}
            disabled={isLoading}
          >
            {isLoading ? 'Retrying...' : 'Try Again'}
          </Button>
        </div>
      </div>
    )
  }

  if (isLoading) {
    return (
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        <div className="text-center">
          <h2 className="text-2xl font-bold text-blue-600 dark:text-blue-400 mb-4">
            Generating Terraform Files...
          </h2>
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600 mx-auto"></div>
        </div>
      </div>
    )
  }

  if (!terraformFiles) {
    return (
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        <div className="text-center">
          <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100 mb-4">
            Configuration Complete
          </h2>
          <p className="text-gray-600 dark:text-gray-400 mb-6">Ready to generate Terraform files</p>
          <Button
            onClick={onRegenerate}
            disabled={isLoading}
          >
            Generate Terraform Files
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="max-w-4xl mx-auto p-6 space-y-6">
      <div className="text-center">
        <p className="text-gray-600 dark:text-gray-400 mb-6">
          Your Confluent Cloud configuration has been converted to Terraform files.
        </p>
      </div>

      <TerraformDisplay terraformFiles={terraformFiles} />

      <Button
        onClick={onRegenerate}
        className="w-full"
        disabled={isLoading}
      >
        {isLoading ? 'Regenerating...' : 'Regenerate Terraform Files'}
      </Button>
    </div>
  )
}
