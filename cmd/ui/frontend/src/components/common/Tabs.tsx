interface TabItem {
  id: string
  label: string
}

type TabSize = 'sm' | 'md' | 'lg'

interface TabsProps {
  tabs: TabItem[]
  activeId: string
  onChange: (id: string) => void
  className?: string
  size?: TabSize
}

const sizeClasses: Record<TabSize, string> = {
  sm: 'text-sm',
  md: 'text-base',
  lg: 'text-lg',
}

export const Tabs = ({ tabs, activeId, onChange, className = '', size = 'sm' }: TabsProps) => {
  const textSizeClass = sizeClasses[size]

  return (
    <div
      className={`bg-white dark:bg-card border-b-2 border-gray-200 dark:border-border ${className}`}
    >
      <nav className="-mb-px flex space-x-8 px-6 pt-6 pb-0 overflow-x-auto">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => onChange(tab.id)}
            className={`py-3 px-1 border-b-2 font-medium ${textSizeClass} transition-colors whitespace-nowrap ${
              activeId === tab.id
                ? 'border-accent text-accent'
                : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-gray-300 dark:hover:border-border'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </nav>
    </div>
  )
}
