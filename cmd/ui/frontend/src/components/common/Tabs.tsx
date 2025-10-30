interface TabItem {
  id: string
  label: string
}

interface TabsProps {
  tabs: TabItem[]
  activeId: string
  onChange: (id: string) => void
  className?: string
}

export default function Tabs({ tabs, activeId, onChange, className = '' }: TabsProps) {
  return (
    <div className={`bg-white dark:bg-gray-800 border-b-2 border-gray-200 dark:border-gray-700 ${className}`}>
      <nav className="-mb-px flex space-x-8 px-6 pt-6 pb-0 overflow-x-auto">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => onChange(tab.id)}
            className={`py-3 px-1 border-b-2 font-medium text-sm transition-colors whitespace-nowrap ${
              activeId === tab.id
                ? 'border-blue-500 text-blue-600 dark:text-blue-400'
                : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:border-gray-300 dark:hover:border-gray-600'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </nav>
    </div>
  )
}

