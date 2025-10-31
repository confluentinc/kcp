import { useState, useEffect, useRef } from 'react'

interface TerraformCodeViewerProps {
  code: string
  language?: string
}

export function TerraformCodeViewer({ code }: TerraformCodeViewerProps) {
  const [height, setHeight] = useState(400)
  const isDragging = useRef(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (isDragging.current && containerRef.current) {
        const rect = containerRef.current.getBoundingClientRect()
        const newHeight = e.clientY - rect.top
        setHeight(Math.max(200, Math.min(800, newHeight)))
      }
    }

    const handleMouseUp = () => {
      isDragging.current = false
    }

    document.addEventListener('mousemove', handleMouseMove)
    document.addEventListener('mouseup', handleMouseUp)

    return () => {
      document.removeEventListener('mousemove', handleMouseMove)
      document.removeEventListener('mouseup', handleMouseUp)
    }
  }, [])

  const handleMouseDown = (e: React.MouseEvent) => {
    e.preventDefault()
    isDragging.current = true
  }

  return (
    <div
      ref={containerRef}
      className="relative"
    >
      <div
        style={{ height: `${height}px` }}
        className="bg-gray-900 text-gray-100 rounded-lg overflow-hidden relative"
      >
        <pre className="h-full overflow-auto p-4 text-sm font-mono">
          <code>{code}</code>
        </pre>
      </div>
      {/* Resize handle */}
      <div
        className="absolute bottom-0 left-0 right-0 h-2 cursor-ns-resize hover:bg-blue-500 transition-colors rounded-b-lg"
        onMouseDown={handleMouseDown}
        title="Drag to resize"
      />
    </div>
  )
}
