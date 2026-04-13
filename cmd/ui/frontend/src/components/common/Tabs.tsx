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
      className={`bg-card border-b border-border ${className}`}
    >
      <nav className="-mb-px flex space-x-8 px-6 pt-6 pb-0 overflow-x-auto">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => onChange(tab.id)}
            className={`py-3.5 px-1 border-b-[3px] font-medium ${textSizeClass} transition-colors duration-150 whitespace-nowrap ${
              activeId === tab.id
                ? 'border-accent text-accent'
                : 'border-transparent text-muted-foreground hover:text-foreground hover:border-border'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </nav>
    </div>
  )
}
