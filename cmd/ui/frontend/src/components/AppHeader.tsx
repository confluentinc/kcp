import { useState, useEffect } from 'react'
import { Header, HeaderSection, HeaderTitle, HeaderSubtitle } from '@/components/ui/header'
import { Button } from '@/components/ui/button'

export default function AppHeader() {
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
          src={darkMode ? '/images/logo-light.svg' : '/images/logo-dark.svg'}
          alt="KCP Logo"
          className="h-6 w-6"
        />
        <HeaderTitle>KCP</HeaderTitle>
        <HeaderSubtitle> - Migrate your Kafka clusters to Confluent Cloud</HeaderSubtitle>
      </HeaderSection>
      <HeaderSection position="right">
        <Button
          onClick={toggleDarkMode}
          variant="outline"
          size="sm"
          className="flex items-center space-x-2"
        >
          <span>{darkMode ? '☀️' : '🌙'}</span>
          <span>{darkMode ? 'Light' : 'Dark'}</span>
        </Button>
      </HeaderSection>
    </Header>
  )
}
