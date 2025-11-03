interface TerraformCodeViewerProps {
  code: string
  language?: string
}

export function TerraformCodeViewer({ code }: TerraformCodeViewerProps) {
  return (
    <div className="relative w-full h-full">
      <div className="bg-card dark:bg-card border border-gray-200 dark:border-border rounded-lg overflow-hidden relative w-full h-full">
        <pre className="h-full w-full overflow-auto p-4 text-sm font-mono text-gray-900 dark:text-gray-100">
          <code className="w-full block">{code}</code>
        </pre>
      </div>
    </div>
  )
}
