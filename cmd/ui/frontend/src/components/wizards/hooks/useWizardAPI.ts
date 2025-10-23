import { useState, useCallback } from 'react'
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

        const response = await fetch(apiEndpoint, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify(wizardData),
        })

        if (response.ok) {
          const files = (await response.json()) as TerraformFiles
          setTerraformFiles(files)
          return files
        } else {
          throw new Error('Failed to generate Terraform files')
        }
      } catch (err) {
        const errorMessage = err instanceof Error ? err.message : 'Unknown error occurred'
        setError(errorMessage)
        console.error('API Error:', err)
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
