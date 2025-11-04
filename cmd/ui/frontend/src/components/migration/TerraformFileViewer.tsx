import { Button } from '@/components/common/ui/button'
import { Copy, Download, FolderOpen } from 'lucide-react'
import { TerraformCodeViewer } from './TerraformCodeViewer'
import { downloadZip, saveZipLocally } from '@/lib/fileSystemUtils'
import type { WizardType } from '@/types'
import type { TerraformFiles } from './wizards/types'

interface TerraformFileViewerProps {
  files: TerraformFiles | null
  clusterName: string
  wizardType: WizardType
  clusterKey: string
  activeFileTabs: Record<string, string>
  setActiveFileTabs: React.Dispatch<React.SetStateAction<Record<string, string>>>
}

export const TerraformFileViewer = ({
  files,
  clusterName,
  wizardType,
  clusterKey,
  activeFileTabs,
  setActiveFileTabs,
}: TerraformFileViewerProps) => {
  if (!files) {
    return <p className="text-gray-600 dark:text-gray-400">No terraform files generated yet.</p>
  }

  const fileEntries = Object.entries(files).filter(([, content]) => content)

  if (fileEntries.length === 0) {
    return <p className="text-gray-600 dark:text-gray-400">No terraform files available.</p>
  }

  const fileTabsKey = `${clusterKey}-${wizardType}`
  const activeFileTab = activeFileTabs[fileTabsKey] || fileEntries[0][0]

  const activeContent = fileEntries.find(([key]) => key === activeFileTab)?.[1] || ''

  const handleCopyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
  }

  return (
    <div className="flex flex-col h-full w-full p-4">
      <div className="bg-white dark:bg-card border-b border-gray-200 dark:border-border flex-shrink-0 pb-0 mb-0">
        <div className="flex items-center justify-between">
          <nav className="-mb-px flex space-x-2 overflow-x-auto flex-1">
            {fileEntries.map(([key]) => (
              <button
                key={key}
                onClick={() => setActiveFileTabs((prev) => ({ ...prev, [fileTabsKey]: key }))}
                className={`py-3 px-4 border-b-2 font-medium text-sm transition-colors whitespace-nowrap ${
                  activeFileTab === key
                    ? 'border-accent text-accent'
                    : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-gray-300 dark:hover:border-border'
                }`}
              >
                {key.replace('_', '.')}
              </button>
            ))}
          </nav>
          <div className="flex items-center gap-1.5 px-2 shrink-0">
            <Button
              size="sm"
              variant="outline"
              onClick={() => handleCopyToClipboard(activeContent)}
              className="text-xs px-2 py-1"
            >
              <Copy className="h-3 w-3 mr-1" />
              Copy
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => downloadZip(files, `${clusterName}-${wizardType}`)}
              className="text-xs px-2 py-1"
            >
              <Download className="h-3 w-3 mr-1" />
              Download ZIP
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => saveZipLocally(files, `${clusterName}-${wizardType}`)}
              className="text-xs px-2 py-1"
            >
              <FolderOpen className="h-3 w-3 mr-1" />
              Save Locally
            </Button>
          </div>
        </div>
      </div>

      <div className="w-full flex-1 min-h-0 mt-0">
        {fileEntries.map(([key, content]) => {
          if (activeFileTab === key && content) {
            return (
              <TerraformCodeViewer
                key={key}
                code={content}
                language="terraform"
              />
            )
          }
          return null
        })}
      </div>
    </div>
  )
}
