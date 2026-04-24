import { useState, useEffect } from 'react'
import { Header, HeaderSection, HeaderTitle } from '@/components/common/ui/header'
import { Button } from '@/components/common/ui/button'
import { Sun, Moon } from 'lucide-react'

interface HeaderTab {
  id: string
  label: string
}

interface AppHeaderProps {
  onFileUpload?: () => void
  isProcessing?: boolean
  error?: string | null
  tabs?: HeaderTab[]
  activeTab?: string
  onTabChange?: (id: string) => void
}

export const AppHeader = ({ onFileUpload, isProcessing = false, error = null, tabs, activeTab, onTabChange }: AppHeaderProps) => {
  const [darkMode, setDarkMode] = useState(true)

  // Initialize dark mode from localStorage or default to dark
  useEffect(() => {
    const stored = localStorage.getItem('darkMode')
    if (stored) {
      setDarkMode(stored === 'true')
    } else {
      setDarkMode(true) // Default to dark mode
    }
  }, [])

  // Apply dark mode to document
  useEffect(() => {
    if (darkMode) {
      document.documentElement.classList.add('dark')
    } else {
      document.documentElement.classList.remove('dark')
    }
    localStorage.setItem('darkMode', darkMode.toString())
  }, [darkMode])

  const toggleDarkMode = () => {
    setDarkMode(!darkMode)
  }

  return (
    <Header
      variant="bordered"
      sticky
    >
      <HeaderSection position="left">
        <img
          src="/images/logo-light.svg"
          alt="KCP Logo"
          className="h-6 w-6"
        />
        <HeaderTitle className={darkMode ? '' : 'text-white'}>KCP</HeaderTitle>
        {tabs && tabs.length > 0 && (
          <nav className="flex items-center ml-6 gap-1">
            {tabs.map((tab) => (
              <button
                key={tab.id}
                onClick={() => onTabChange?.(tab.id)}
                className={`px-3 py-1.5 text-sm font-medium transition-colors duration-150 border-b-2 ${
                  activeTab === tab.id
                    ? 'text-white border-accent'
                    : 'text-white/60 border-transparent hover:text-white'
                }`}
              >
                {tab.label}
              </button>
            ))}
          </nav>
        )}
      </HeaderSection>
      <HeaderSection position="right">
        {error && (
          <div className="mr-4 p-2 bg-destructive/10 border border-destructive/20 rounded-md">
            <div className="text-sm text-destructive">
              <strong>Error:</strong> {error}
            </div>
          </div>
        )}
        {onFileUpload && (
          <Button
            onClick={onFileUpload}
            variant="outline"
            size="sm"
            disabled={isProcessing}
            className={`mr-2 ${darkMode ? '' : 'border-white/30 text-white hover:bg-white/10 bg-transparent'}`}
          >
            {isProcessing ? 'Processing...' : 'Upload KCP State File'}
          </Button>
        )}
        <Button
          onClick={toggleDarkMode}
          variant="ghost"
          size="icon"
          className={darkMode ? '' : 'text-white hover:bg-white/10'}
        >
          {darkMode ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
        </Button>
      </HeaderSection>
    </Header>
  )
}
