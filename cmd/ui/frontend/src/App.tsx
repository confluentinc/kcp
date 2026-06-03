import './App.css'
import { Home } from './pages/home'
import { GovBanner } from './components/GovBanner'

export const App = () => {
  return (
    <div className="h-svh flex flex-col overflow-hidden">
      <GovBanner />
      <div className="flex-1 min-h-0">
        <Home />
      </div>
    </div>
  )
}
