import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatRetentionTime(ms: string): {
  seconds: number
  minutes: number
  hours: number
  days: number
} {
  if (ms === '-1') {
    return { seconds: -1, minutes: -1, hours: -1, days: -1 }
  }

  const milliseconds = parseInt(ms)
  const seconds = Math.floor(milliseconds / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)

  return { seconds, minutes, hours, days }
}

/**
 * Downloads data as a CSV file
 * @param csvData - The CSV content as a string
 * @param filename - The filename without extension (e.g., 'metrics-cluster-region')
 */
export function downloadCSV(csvData: string, filename: string): void {
  const blob = new Blob([csvData], { type: 'text/csv' })
  const url = window.URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${filename}.csv`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  window.URL.revokeObjectURL(url)
}

/**
 * Downloads data as a JSON file
 * @param jsonData - The JSON data object or string
 * @param filename - The filename without extension (e.g., 'metrics-cluster-region')
 */
export function downloadJSON(jsonData: any, filename: string): void {
  const jsonString = typeof jsonData === 'string' ? jsonData : JSON.stringify(jsonData, null, 2)
  const blob = new Blob([jsonString], { type: 'application/json' })
  const url = window.URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${filename}.json`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  window.URL.revokeObjectURL(url)
}

/**
 * Generates a filename for metrics downloads
 * @param clusterName - The name of the cluster
 * @param region - The region name (optional)
 * @returns A formatted filename string
 */
export function generateMetricsFilename(clusterName: string, region?: string): string {
  const cleanClusterName = clusterName.replace(/[^a-zA-Z0-9-_]/g, '-')
  const cleanRegion = region ? region.replace(/[^a-zA-Z0-9-_]/g, '-') : 'unknown'
  return `metrics-${cleanClusterName}-${cleanRegion}`
}
