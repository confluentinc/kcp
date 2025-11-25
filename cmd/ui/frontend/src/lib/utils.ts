import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'
import type { TerraformFiles, TreeNode } from '@/components/migration/wizards/types'

/**
 * Utility function to merge Tailwind CSS classes with clsx and tailwind-merge.
 * Combines class names and resolves conflicting Tailwind utilities.
 *
 * @param {...ClassValue} inputs - Class names, objects, or arrays to merge
 * @returns {string} Merged class string with conflicts resolved
 */
export const cn = (...inputs: ClassValue[]) => {
  return twMerge(clsx(inputs))
}

/**
 * Converts milliseconds to time units (seconds, minutes, hours, days).
 * Handles special case of '-1' which represents infinite retention.
 *
 * @param {string} ms - Milliseconds as a string (or '-1' for infinite)
 * @returns {Object} Time units object with seconds, minutes, hours, and days
 * @returns {number} return.seconds - Total seconds
 * @returns {number} return.minutes - Total minutes
 * @returns {number} return.hours - Total hours
 * @returns {number} return.days - Total days
 */
export const formatRetentionTime = (
  ms: string
): {
  seconds: number
  minutes: number
  hours: number
  days: number
} => {
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
export const downloadCSV = (csvData: string, filename: string): void => {
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
export const downloadJSON = (jsonData: unknown, filename: string): void => {
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
export const generateMetricsFilename = (clusterName: string, region?: string): string => {
  const cleanClusterName = clusterName.replace(/[^a-zA-Z0-9-_]/g, '-')
  const cleanRegion = region ? region.replace(/[^a-zA-Z0-9-_]/g, '-') : 'unknown'
  return `metrics-${cleanClusterName}-${cleanRegion}`
}

/**
 * Generates a filename for costs downloads
 * @param region - The region name
 * @returns A formatted filename string
 */
export const generateCostsFilename = (region: string): string => {
  const cleanRegion = region.replace(/[^a-zA-Z0-9-_]/g, '-')
  return `costs-${cleanRegion}`
}

/**
 * Helper function to create StatusBadge props from an enabled boolean
 * @param enabled - Whether the status is enabled
 * @param enabledLabel - Label to show when enabled (default: 'Enabled')
 * @param disabledLabel - Label to show when disabled (default: 'Disabled')
 * @returns Props object for StatusBadge component
 */
export const createStatusBadgeProps = (
  enabled: boolean,
  enabledLabel: string = 'Enabled',
  disabledLabel: string = 'Disabled'
): { enabled: boolean; label: string } => {
  return {
    enabled,
    label: enabled ? enabledLabel : disabledLabel,
  }
}

/**
 * Converts hierarchical TerraformFiles structure into a flat array of tree nodes for react-arborist
 * @param files - The TerraformFiles object from the API
 * @returns Array of TreeNode objects representing the file structure
 */
export const convertTerraformFilesToTree = (files: TerraformFiles | null): TreeNode[] => {
  if (!files) return []

  const treeNodes: TreeNode[] = []

  // Root level files
  const rootFiles: Array<keyof TerraformFiles> = [
    'main.tf',
    'providers.tf',
    'variables.tf',
    'outputs.tf',
    'inputs.auto.tfvars',
  ]

  rootFiles.forEach((fileName) => {
    const content = files[fileName]
    if (content && typeof content === 'string') {
      treeNodes.push({
        id: fileName,
        name: fileName,
        content,
        isFolder: false,
      })
    }
  })

  // Module folders and their files
  if (files.modules && Array.isArray(files.modules)) {
    files.modules.forEach((module) => {
      const moduleChildren: TreeNode[] = []

      // Module's standard files
      const moduleFiles: Array<'main.tf' | 'variables.tf' | 'outputs.tf' | 'versions.tf' | 'providers.tf' | 'inputs.auto.tfvars'> = [
        'main.tf',
        'variables.tf',
        'outputs.tf',
        'versions.tf',

        // additional files for scripts flow
        'providers.tf',
        'inputs.auto.tfvars',
      ]

      moduleFiles.forEach((fileName) => {
        const content = module[fileName]
        if (content) {
          moduleChildren.push({
            id: `${module.name}/${fileName}`,
            name: fileName,
            content,
            isFolder: false,
          })
        }
      })

      // Additional files in the module
      if (module.additional_files) {
        Object.entries(module.additional_files).forEach(([fileName, content]) => {
          moduleChildren.push({
            id: `${module.name}/${fileName}`,
            name: fileName,
            content,
            isFolder: false,
          })
        })
      }

      // Add module folder with its children
      if (moduleChildren.length > 0) {
        treeNodes.push({
          id: module.name,
          name: module.name,
          children: moduleChildren,
          isFolder: true,
        })
      }
    })
  }

  return treeNodes
}

/**
 * Flattens tree structure to get all file nodes with their content
 * @param nodes - Array of TreeNode objects
 * @returns Map of file ID to content
 */
export const flattenTreeNodes = (nodes: TreeNode[]): Map<string, string> => {
  const fileMap = new Map<string, string>()

  const traverse = (node: TreeNode) => {
    if (!node.isFolder && node.content) {
      fileMap.set(node.id, node.content)
    }
    if (node.children) {
      node.children.forEach(traverse)
    }
  }

  nodes.forEach(traverse)
  return fileMap
}

/**
 * Converts TerraformFiles to flat structure for download/save utilities
 * @param files - The TerraformFiles object from the API
 * @returns Record of file paths to content
 */
export const flattenTerraformFiles = (files: TerraformFiles | null): Record<string, string> => {
  if (!files) return {}

  const flatFiles: Record<string, string> = {}

  // Root level files
  const rootFiles: Array<keyof TerraformFiles> = [
    'main.tf',
    'providers.tf',
    'variables.tf',
    'outputs.tf',
    'inputs.auto.tfvars',
  ]

  rootFiles.forEach((fileName) => {
    const content = files[fileName]
    if (content && typeof content === 'string') {
      flatFiles[fileName] = content
    }
  })

  // Module files
  if (files.modules && Array.isArray(files.modules)) {
    files.modules.forEach((module) => {
      const moduleFiles: Array<'main.tf' | 'variables.tf' | 'outputs.tf' | 'versions.tf' | 'providers.tf' | 'inputs.auto.tfvars'> = [
        'main.tf',
        'variables.tf',
        'outputs.tf',
        'versions.tf',

        // additional files for scripts flow
        'providers.tf',
        'inputs.auto.tfvars',
      ]

      moduleFiles.forEach((fileName) => {
        const content = module[fileName]
        if (content) {
          flatFiles[`${module.name}/${fileName}`] = content
        }
      })

      // Additional files
      if (module.additional_files) {
        Object.entries(module.additional_files).forEach(([fileName, content]) => {
          flatFiles[`${module.name}/${fileName}`] = content
        })
      }
    })
  }

  return flatFiles
}
