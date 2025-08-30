import { useState } from 'react'
import './App.css'

import { Button } from '@/components/ui/button'

function App() {
  const [healthStatus, setHealthStatus] = useState<string>('')
  const [isLoading, setIsLoading] = useState(false)

  const checkHealth = async () => {
    setIsLoading(true)
    try {
      const response = await fetch('/health')
      const data = await response.json()
      setHealthStatus(`Status: ${data.status}, Service: ${data.service}, Time: ${data.timestamp}`)
    } catch (error) {
      setHealthStatus(`Error: ${error instanceof Error ? error.message : 'Unknown error'}`)
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <>
      <div className="flex min-h-svh flex-col items-center justify-center gap-4">
        <Button
          onClick={checkHealth}
          disabled={isLoading}
        >
          {isLoading ? 'Checking...' : 'Health Check'}
        </Button>
        {healthStatus && (
          <p className="text-sm text-muted-foreground max-w-md text-center">{healthStatus}</p>
        )}
      </div>
    </>
  )
}

export default App
