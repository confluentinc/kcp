import { useCallback } from 'react'
import { downloadCSV, downloadJSON } from '@/lib/utils'

interface UseDownloadHandlersProps {
  csvData: string
  jsonData: unknown
  filename: string
}

/**
 * Hook to provide consistent download handlers for CSV and JSON data
 * Eliminates duplicate download logic across components
 *
 * @param csvData - The CSV data string to download
 * @param jsonData - The JSON data object to download
 * @param filename - The base filename (without extension) for downloads
 * @returns Object containing handleDownloadCSV and handleDownloadJSON functions
 */
export function useDownloadHandlers({ csvData, jsonData, filename }: UseDownloadHandlersProps) {
  const handleDownloadCSV = useCallback(() => {
    downloadCSV(csvData, filename)
  }, [csvData, filename])

  const handleDownloadJSON = useCallback(() => {
    downloadJSON(jsonData, filename)
  }, [jsonData, filename])

  return {
    handleDownloadCSV,
    handleDownloadJSON,
  }
}
