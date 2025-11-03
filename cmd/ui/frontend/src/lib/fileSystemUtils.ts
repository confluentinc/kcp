/**
 * File System Access API utilities for saving and downloading files.
 * Provides reusable functions for ZIP creation, browser downloads, and
 * File System API integration (when supported).
 */

// ============================================================================
// Type Definitions for File System Access API
// ============================================================================

declare global {
  interface Window {
    showDirectoryPicker(options?: {
      mode?: 'read' | 'readwrite'
      startIn?: 'desktop' | 'downloads' | 'documents'
    }): Promise<FileSystemDirectoryHandle>
  }

  interface FileSystemDirectoryHandle {
    getFileHandle(name: string, options?: { create?: boolean }): Promise<FileSystemFileHandle>
  }

  interface FileSystemFileHandle {
    createWritable(): Promise<FileSystemWritableFileStream>
  }

  interface FileSystemWritableFileStream {
    write(data: string | BufferSource | Blob): Promise<void>
    close(): Promise<void>
  }
}

// ============================================================================
// ZIP File Creation
// ============================================================================

/**
 * Creates a ZIP blob from a collection of files.
 * 
 * @param files - Object mapping file names to file contents
 * @returns Promise resolving to a Blob containing the ZIP archive
 * @throws Error if ZIP creation fails
 * 
 * @example
 * ```typescript
 * const files = {
 *   'main_tf': 'resource "aws_instance" "example" { ... }',
 *   'variables_tf': 'variable "region" { ... }'
 * }
 * const zipBlob = await createZipBlob(files)
 * ```
 */
export const createZipBlob = async (files: Record<string, string | undefined>): Promise<Blob> => {
  try {
    const { default: JSZip } = await import('jszip')
    const zip = new JSZip()

    for (const [key, content] of Object.entries(files)) {
      if (content) {
        const fileName = key.replace('_', '.')
        zip.file(fileName, content)
      }
    }

    return await zip.generateAsync({ type: 'blob' })
  } catch {
    throw new Error('Failed to create zip file')
  }
}

// ============================================================================
// Browser Download (Standard)
// ============================================================================

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
        const formattedFileName = key.replace('_', '.')
        zip.file(formattedFileName, content)
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

// ============================================================================
// File System Access API (Advanced Save)
// ============================================================================

/**
 * Saves a ZIP file to a user-selected directory using the File System Access API.
 * This allows users to choose exactly where to save the file.
 * Falls back gracefully if the API is not supported.
 * 
 * @param files - Object mapping file keys to file contents
 * @param fileName - Base name for the ZIP file (without extension)
 * @returns Promise that resolves when save is complete
 * 
 * @example
 * ```typescript
 * await saveZipLocally(terraformFiles, 'my-cluster-target-infra')
 * // User picks directory, then saves: my-cluster-target-infra.zip
 * ```
 */
export const saveZipLocally = async (
  files: Record<string, string | undefined>,
  fileName: string
): Promise<void> => {
  try {
    // Check if File System API is supported
    if (!('showDirectoryPicker' in window)) {
      alert(
        'Your browser does not support saving files to specific locations. Please use "Download ZIP" instead.'
      )
      return
    }

    // Filter files with content
    const fileEntries = Object.entries(files).filter(([, content]) => content)

    if (fileEntries.length === 0) {
      alert('No files to download')
      return
    }

    // Create zip blob
    const blob = await createZipBlob(files)
    const zipFileName = `${fileName}.zip`

    // Use File System API to save to user-selected directory
    const directoryHandle = await window.showDirectoryPicker({
      mode: 'readwrite',
      startIn: 'downloads',
    })

    // Save the zip file to the selected directory
    const fileHandle = await directoryHandle.getFileHandle(zipFileName, { create: true })
    const writable = await fileHandle.createWritable()
    await writable.write(blob)
    await writable.close()

    alert(`Successfully saved ${zipFileName} to your selected directory!`)
  } catch (error: unknown) {
    // User canceled the picker or other error
    const err = error as { name?: string; message?: string; code?: string }
    if (err.name === 'AbortError' || err.message === 'The user aborted a request.') {
      // User canceled directory selection - silent fail
    } else if (err.message?.includes('system files') || err.code === 'InvalidModificationError') {
      alert(
        'Cannot save to this directory. Please select a different folder (e.g., Desktop, Documents, or a subfolder).'
      )
    } else {
      alert('Failed to save files. Please try again or use "Download ZIP" instead.')
    }
  }
}

