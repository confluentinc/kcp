/**
 * File System utilities for downloading files.
 * Provides functions for ZIP creation and browser downloads.
 */

/**
 * Downloads a ZIP file containing multiple files to the browser's download folder.
 * Uses standard browser download API (works in all modern browsers).
 *
 * @param files - Object mapping file keys to file contents
 * @param fileName - Base name for the ZIP file (without extension)
 * @returns Promise that resolves when download is complete
 *
 * @example
 * ```typescript
 * await downloadZip(terraformFiles, 'my-cluster-target-infra')
 * // Downloads: my-cluster-target-infra.zip
 * ```
 */
export const downloadZip = async (
  files: Record<string, string | undefined>,
  fileName: string
): Promise<void> => {
  try {
    // Filter files with content
    const fileEntries = Object.entries(files).filter(([, content]) => content)

    if (fileEntries.length === 0) {
      alert('No files to download')
      return
    }

    // Dynamically import JSZip only when needed
    const { default: JSZip } = await import('jszip')

    // Create zip file
    const zip = new JSZip()

    // Add files to zip
    for (const [key, content] of fileEntries) {
      if (content) {
        zip.file(key, content)
      }
    }

    // Generate zip blob
    const blob = await zip.generateAsync({ type: 'blob' })
    const zipFileName = `${fileName}.zip`

    // Download directly to browser's download folder
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = zipFileName
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    window.URL.revokeObjectURL(url)
  } catch {
    alert('Failed to create zip file. Please try again.')
  }
}
