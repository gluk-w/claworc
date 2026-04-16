import {
  useState,
  useId,
  useCallback,
  useRef,
  Children,
  isValidElement,
  cloneElement,
  type ReactNode,
  type ReactElement,
  type KeyboardEvent,
} from 'react';
import { cn } from '@/lib/cn';

export interface Tab {
  id: string;
  label: string;
}

interface TabsProps {
  /** Array of tab definitions */
  tabs: Tab[];
  /** Default selected tab ID (uncontrolled mode) */
  defaultValue?: string;
  /** Current selected tab ID (controlled mode) */
  value?: string;
  /** Callback when tab changes */
  onChange?: (id: string) => void;
  /** Additional CSS classes */
  className?: string;
  /** Tab panel content - use data-tab-content="id" attribute on children */
  children: ReactNode;
}

/**
 * Tabs Component
 *
 * Accessible horizontal tabbed interface with keyboard navigation.
 * Supports both controlled and uncontrolled modes.
 *
 * @example
 * <Tabs tabs={[{ id: 'tab1', label: 'Tab 1' }, { id: 'tab2', label: 'Tab 2' }]}>
 *   <div data-tab-content="tab1">Content for tab 1</div>
 *   <div data-tab-content="tab2">Content for tab 2</div>
 * </Tabs>
 */
export function Tabs({
  tabs,
  defaultValue,
  value,
  onChange,
  className,
  children,
}: TabsProps) {
  const generatedId = useId();
  const isControlled = value !== undefined;
  const [internalValue, setInternalValue] = useState(defaultValue ?? tabs[0]?.id ?? '');
  const tablistRef = useRef<HTMLDivElement>(null);

  const activeTabId = isControlled ? value : internalValue;

  const handleTabChange = useCallback(
    (id: string) => {
      if (!isControlled) {
        setInternalValue(id);
      }
      onChange?.(id);
    },
    [isControlled, onChange],
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLButtonElement>, currentIndex: number) => {
      let newIndex = currentIndex;

      switch (e.key) {
        case 'ArrowLeft':
          newIndex = currentIndex > 0 ? currentIndex - 1 : tabs.length - 1;
          break;
        case 'ArrowRight':
          newIndex = currentIndex < tabs.length - 1 ? currentIndex + 1 : 0;
          break;
        case 'Home':
          newIndex = 0;
          break;
        case 'End':
          newIndex = tabs.length - 1;
          break;
        default:
          return;
      }

      e.preventDefault();

      const newTab = tabs[newIndex];
      if (newTab) {
        handleTabChange(newTab.id);
        const tabButton = document.getElementById(`tab-${generatedId}-${newTab.id}`);
        tabButton?.focus();
      }
    },
    [tabs, generatedId, handleTabChange],
  );

  // Process children to add panel attributes and visibility control
  const contentPanels = Children.map(children, (child) => {
    if (!isValidElement(child)) return null;

    const tabContentId = (child.props as Record<string, unknown>)?.['data-tab-content'] as
      | string
      | undefined;
    if (!tabContentId) return child;

    const isActive = tabContentId === activeTabId;

    return cloneElement(child as ReactElement<Record<string, unknown>>, {
      role: 'tabpanel',
      id: `panel-${generatedId}-${tabContentId}`,
      'aria-labelledby': `tab-${generatedId}-${tabContentId}`,
      tabIndex: 0,
      hidden: isActive ? undefined : true,
      className: cn(
        (child.props as Record<string, string | undefined>)?.className,
        'outline-none rounded-md',
        'focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
      ),
    });
  });

  return (
    <div className={cn('w-full', className)}>
      {/* Tab List */}
      <div
        ref={tablistRef}
        className="flex border-b border-border"
        role="tablist"
        aria-label="Tabs"
      >
        {tabs.map((tab, index) => {
          const isActive = tab.id === activeTabId;

          return (
            <button
              key={tab.id}
              id={`tab-${generatedId}-${tab.id}`}
              type="button"
              role="tab"
              aria-selected={isActive}
              aria-controls={`panel-${generatedId}-${tab.id}`}
              tabIndex={isActive ? 0 : -1}
              onClick={() => handleTabChange(tab.id)}
              onKeyDown={(e) => handleKeyDown(e, index)}
              className={cn(
                'px-4 py-2.5 text-sm font-medium -mb-px border-b-2 transition-colors',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
                isActive
                  ? 'border-foreground text-foreground'
                  : 'border-transparent text-foreground-muted hover:text-foreground hover:border-border',
              )}
            >
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Tab Panels Container */}
      <div className="mt-4">{contentPanels}</div>
    </div>
  );
}

export default Tabs;
