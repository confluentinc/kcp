import { useState, useMemo, useRef, useEffect } from 'react'
import { Tree } from 'react-arborist'
import { Button } from '@/components/common/ui/button'
import { Download, File, Folder, ChevronRight } from 'lucide-react'
import { TerraformCodeViewer } from './TerraformCodeViewer'
import { downloadZip } from '@/lib/fileSystemUtils'
import { convertTerraformFilesToTree, flattenTerraformFiles } from '@/lib/utils'
import type { WizardType } from '@/types'
import type { TerraformFiles, TreeNode } from './wizards/types'

interface TerraformFileViewerProps {
  files: TerraformFiles | null
  clusterName: string
  wizardType: WizardType
}

export const TerraformFileViewer = ({
  files,
  clusterName,
  wizardType,
}: TerraformFileViewerProps) => {
  const [selectedFileId, setSelectedFileId] = useState<string | null>(null)
  const [treeDimensions, setTreeDimensions] = useState({ width: 320, height: 500 })
  const treeContainerRef = useRef<HTMLDivElement>(null)

  const treeData = useMemo(() => convertTerraformFilesToTree(files), [files])
  const flatFiles = useMemo(() => flattenTerraformFiles(files), [files])

  // Measure tree container dimensions
  useEffect(() => {
    const updateDimensions = () => {
      if (treeContainerRef.current) {
        const { width, height } = treeContainerRef.current.getBoundingClientRect()
        setTreeDimensions({ width, height })
      }
    }

    updateDimensions()

    const resizeObserver = new ResizeObserver(updateDimensions)
    if (treeContainerRef.current) {
      resizeObserver.observe(treeContainerRef.current)
    }

    return () => {
      resizeObserver.disconnect()
    }
  }, [])

  if (!files) {
    return <p className="text-gray-600 dark:text-gray-400">No terraform files generated yet.</p>
  }

  if (treeData.length === 0) {
    return <p className="text-gray-600 dark:text-gray-400">No terraform files available.</p>
  }

  // Find first file if none selected
  const getFirstFile = (nodes: TreeNode[]): string | null => {
    for (const node of nodes) {
      if (!node.isFolder && node.content) {
        return node.id
      }
      if (node.children) {
        const childFile = getFirstFile(node.children)
        if (childFile) return childFile
      }
    }
    return null
  }

  const currentFileId = selectedFileId || getFirstFile(treeData)
  const selectedContent = currentFileId ? flatFiles[currentFileId] || '' : ''

  return (
    <div className="flex h-full w-full">
      {/* Left Panel - Tree View */}
      <div className="w-80 border-r border-gray-200 dark:border-border bg-white dark:bg-card flex flex-col">
        <div className="p-3 border-b border-gray-200 dark:border-border flex-shrink-0">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Files</h3>
        </div>
        <div
          ref={treeContainerRef}
          className="flex-1 min-h-0"
        >
          <Tree
            data={treeData}
            openByDefault={false}
            width={treeDimensions.width}
            height={treeDimensions.height}
            indent={20}
            rowHeight={32}
            overscanCount={10}
            onSelect={(nodes) => {
              if (nodes.length > 0) {
                const node = nodes[0]
                if (!node.data.isFolder) {
                  setSelectedFileId(node.data.id)
                }
              }
            }}
          >
            {({ node, style, dragHandle }) => {
              // Calculate padding: root items get 12px, nested items get 12px + 32px per level
              const leftPadding = node.level === 0 ? 12 : 12 + node.level * 32

              return (
                <div
                  style={{
                    ...style,
                    paddingLeft: `${leftPadding}px`,
                  }}
                  ref={dragHandle}
                  className={`flex items-center py-1 pr-3 cursor-pointer hover:bg-gray-100 dark:hover:bg-gray-800 ${
                    currentFileId === node.data.id ? 'bg-accent/10' : ''
                  }`}
                  onClick={() => {
                    if (node.data.isFolder) {
                      node.toggle()
                    } else {
                      setSelectedFileId(node.data.id)
                    }
                  }}
                >
                  {node.data.isFolder && (
                    <ChevronRight
                      className={`h-4 w-4 mr-1 flex-shrink-0 transition-transform ${
                        node.isOpen ? 'rotate-90' : ''
                      }`}
                    />
                  )}
                  {node.data.isFolder ? (
                    <Folder className="h-4 w-4 mr-2 flex-shrink-0 text-blue-500" />
                  ) : (
                    <File className="h-4 w-4 mr-2 flex-shrink-0 text-gray-500" />
                  )}
                  <span className="text-sm text-gray-900 dark:text-gray-100 truncate">
                    {node.data.name}
                  </span>
                </div>
              )
            }}
          </Tree>
        </div>
      </div>

      {/* Right Panel - Code Viewer */}
      <div className="flex-1 flex flex-col bg-gray-50 dark:bg-background">
        <div className="flex items-center justify-between p-3 bg-white dark:bg-card border-b border-gray-200 dark:border-border">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
            {currentFileId || 'No file selected'}
          </h3>
          <Button
            size="sm"
            variant="outline"
            onClick={() => downloadZip(flatFiles, `${clusterName}-${wizardType}`)}
            className="text-xs px-2 py-1"
          >
            <Download className="h-3 w-3 mr-1" />
            Download ZIP
          </Button>
        </div>
        <div className="flex-1 overflow-hidden p-4">
          {selectedContent ? (
            <TerraformCodeViewer
              code={selectedContent}
              language="terraform"
            />
          ) : (
            <p className="text-gray-600 dark:text-gray-400">Select a file to view its contents</p>
          )}
        </div>
      </div>
    </div>
  )
}
