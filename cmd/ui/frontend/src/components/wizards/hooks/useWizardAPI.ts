import { useState, useCallback } from 'react'
import { apiClient } from '@/services/apiClient'
import type { TerraformFiles } from '../types'

export function useWizardAPI(apiEndpoint: string) {
  const [isLoading, setIsLoading] = useState(false)
  const [terraformFiles, setTerraformFiles] = useState<TerraformFiles | null>(null)
  const [error, setError] = useState<string | null>(null)

  const generateTerraform = useCallback(
    async (wizardData: Record<string, any>) => {
      try {
        setIsLoading(true)
        setError(null)

        const files = await apiClient.wizard.generateTerraform<TerraformFiles>(
          apiEndpoint,
          wizardData
        )

        setTerraformFiles(files)
        return files
      } catch (err) {
        const errorMessage =
          err instanceof Error ? err.message : 'Failed to generate Terraform files'
        setError(errorMessage)
        // API Error - error is thrown and can be handled by caller
        throw err
      } finally {
        setIsLoading(false)
      }
    },
    [apiEndpoint]
  )

  const reset = useCallback(() => {
    setTerraformFiles(null)
    setError(null)
    setIsLoading(false)
  }, [])

  return {
    isLoading,
    terraformFiles,
    error,
    generateTerraform,
    reset,
  }
}
