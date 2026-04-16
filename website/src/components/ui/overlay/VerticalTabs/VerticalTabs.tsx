import { useState, useId, useCallback, useRef, useEffect, Children, isValidElement, cloneElement, type ReactNode, type ReactElement, type KeyboardEvent } from 'react';
import { cn } from '@/lib/cn';
import { ChevronDown } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

export interface VerticalTab {
  id: string;
  label: string;
  description?: string;
  icon?: LucideIcon;
}

interface VerticalTabsProps {
  /** Array of tab definitions */
  tabs: VerticalTab[];
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
  /** Mobile dropdown variant: 'dropdown' (default) or 'sheet' (bottom sheet) */
  mobileVariant?: 'dropdown' | 'sheet';
}

/**
 * VerticalTabs Component
 *
 * Responsive vertical tabs with:
 * - Desktop (lg+): Vertical sidebar with tabs on left, content on right
 * - Mobile (< lg): Select dropdown to choose tabs, full-width content below
 *
 * @example
 * <VerticalTabs tabs={[{ id: 'a', label: 'Tab A' }, { id: 'b', label: 'Tab B' }]}>
 *   <div data-tab-content="a">Content A</div>
 *   <div data-tab-content="b">Content B</div>
 * </VerticalTabs>
 */
export function VerticalTabs({
  tabs,
  defaultValue,
  value,
  onChange,
  className,
  children,
  mobileVariant = 'dropdown',
}: VerticalTabsProps) {
  const generatedId = useId();
  const isControlled = value !== undefined;
  const [internalValue, setInternalValue] = useState(defaultValue ?? tabs[0]?.id ?? '');
  const [isDropdownOpen, setIsDropdownOpen] = useState(false);
  const [isClosing, setIsClosing] = useState(false);
  const [focusedIndex, setFocusedIndex] = useState(-1);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  const activeTabId = isControlled ? value : internalValue;
  const activeTabData = tabs.find(t => t.id === activeTabId);
  const ActiveIcon = activeTabData?.icon;

  // Close dropdown with exit animation
  const closeDropdown = useCallback(() => {
    if (!isDropdownOpen || isClosing) return;
    setIsClosing(true);
    setTimeout(() => {
      setIsDropdownOpen(false);
      setIsClosing(false);
    }, 200); // Match animation duration
  }, [isDropdownOpen, isClosing]);

  const handleTabChange = useCallback((id: string) => {
    if (!isControlled) {
      setInternalValue(id);
    }
    onChange?.(id);
    closeDropdown();
  }, [isControlled, onChange, closeDropdown]);

  // Close dropdown on outside click
  useEffect(() => {
    if (!isDropdownOpen) return;

    const handleClickOutside = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        closeDropdown();
      }
    };

    const handleEscape = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape') {
        closeDropdown();
        triggerRef.current?.focus();
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    document.addEventListener('keydown', handleEscape);

    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
      document.removeEventListener('keydown', handleEscape);
    };
  }, [isDropdownOpen, closeDropdown]);

  // Reset focused index when dropdown closes
  useEffect(() => {
    if (!isDropdownOpen) {
      setFocusedIndex(-1);
    } else {
      // Set initial focus to active tab
      const activeIndex = tabs.findIndex(t => t.id === activeTabId);
      setFocusedIndex(activeIndex >= 0 ? activeIndex : 0);
    }
  }, [isDropdownOpen, tabs, activeTabId]);

  const handleDropdownKeyDown = useCallback((e: KeyboardEvent<HTMLButtonElement | HTMLDivElement>) => {
    if (!isDropdownOpen) {
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
        e.preventDefault();
        setIsDropdownOpen(true);
      }
      return;
    }

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setFocusedIndex(prev => (prev < tabs.length - 1 ? prev + 1 : 0));
        break;
      case 'ArrowUp':
        e.preventDefault();
        setFocusedIndex(prev => (prev > 0 ? prev - 1 : tabs.length - 1));
        break;
      case 'Home':
        e.preventDefault();
        setFocusedIndex(0);
        break;
      case 'End':
        e.preventDefault();
        setFocusedIndex(tabs.length - 1);
        break;
      case 'Enter':
      case ' ':
        e.preventDefault();
        if (focusedIndex >= 0 && tabs[focusedIndex]) {
          handleTabChange(tabs[focusedIndex].id);
          triggerRef.current?.focus();
        }
        break;
      case 'Tab':
        closeDropdown();
        break;
    }
  }, [isDropdownOpen, tabs, focusedIndex, handleTabChange, closeDropdown]);

  const handleDesktopKeyDown = useCallback((e: KeyboardEvent<HTMLButtonElement>, currentIndex: number) => {
    let newIndex = currentIndex;

    switch (e.key) {
      case 'ArrowUp':
        e.preventDefault();
        newIndex = currentIndex > 0 ? currentIndex - 1 : tabs.length - 1;
        break;
      case 'ArrowDown':
        e.preventDefault();
        newIndex = currentIndex < tabs.length - 1 ? currentIndex + 1 : 0;
        break;
      case 'Home':
        e.preventDefault();
        newIndex = 0;
        break;
      case 'End':
        e.preventDefault();
        newIndex = tabs.length - 1;
        break;
      default:
        return;
    }

    const newTab = tabs[newIndex];
    if (newTab) {
      if (!isControlled) {
        setInternalValue(newTab.id);
      }
      onChange?.(newTab.id);
      // Focus the new tab button
      const tabButton = document.getElementById(`vtab-${generatedId}-${newTab.id}`);
      tabButton?.focus();
    }
  }, [tabs, generatedId, isControlled, onChange]);

  // Process children to add visibility control - render only once
  const contentPanels = Children.map(children, (child) => {
    if (!isValidElement(child)) return null;

    const tabContentId = (child.props as Record<string, unknown>)?.['data-tab-content'] as string | undefined;
    if (!tabContentId) return child;

    const isActive = tabContentId === activeTabId;

    return cloneElement(child as ReactElement<Record<string, unknown>>, {
      role: 'tabpanel',
      id: `vtab-panel-${generatedId}-${tabContentId}`,
      'aria-labelledby': `vtab-${generatedId}-${tabContentId}`,
      tabIndex: 0,
      hidden: isActive ? undefined : true,
      className: cn(
        (child.props as Record<string, string | undefined>)?.className,
        'outline-none rounded-md',
        'focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2'
      ),
    });
  });

  // Mobile dropdown option renderer
  // Design: Sculptural depth with semantic token system
  const renderMobileOption = (tab: VerticalTab, index: number, isSheet: boolean) => {
    const isActive = tab.id === activeTabId;
    const isFocused = index === focusedIndex;
    const Icon = tab.icon;

    return (
      <button
        key={tab.id}
        type="button"
        role="option"
        aria-selected={isActive}
        onClick={() => handleTabChange(tab.id)}
        className={cn(
          'group relative w-full flex items-center gap-3 p-4 text-left',
          'transition-all duration-200',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-1',
          isSheet ? 'rounded-xl' : 'rounded-lg',
          isActive
            ? cn(
                // Inset effect using semantic tokens
                'bg-secondary/80',
                'shadow-[inset_0_1px_3px_rgba(0,0,0,0.08)]',
                'dark:shadow-[inset_0_1px_3px_rgba(0,0,0,0.25)]'
              )
            : cn(
                'hover:bg-background-secondary',
                'hover:translate-x-0.5',
                isFocused && 'bg-background-secondary/60'
              )
        )}
      >
        {/* Icon container with glow effect */}
        {Icon && (
          <div
            className={cn(
              'relative flex items-center justify-center',
              'w-10 h-10 rounded-xl shrink-0',
              'transition-all duration-200',
              isActive
                ? cn(
                    'bg-brand-100 dark:bg-brand-900/40',
                    'ring-1 ring-brand-200 dark:ring-brand-800'
                  )
                : cn(
                    'bg-secondary',
                    'group-hover:bg-secondary-hover',
                    'group-hover:scale-105',
                    'scale-[0.97]'
                  )
            )}
          >
            <Icon
              className={cn(
                'h-5 w-5 transition-all duration-200',
                isActive
                  ? 'text-brand-600 dark:text-brand-400 scale-105'
                  : 'text-foreground-muted group-hover:text-foreground-secondary'
              )}
              strokeWidth={isActive ? 2.25 : 1.75}
            />
          </div>
        )}

        {/* Fallback indicator when no icon */}
        {!Icon && (
          <div
            className={cn(
              'w-10 h-10 rounded-xl shrink-0 flex items-center justify-center',
              'transition-all duration-200',
              isActive
                ? 'bg-brand-100 dark:bg-brand-900/40'
                : 'bg-secondary'
            )}
          >
            <div
              className={cn(
                'rounded-full transition-all duration-200',
                isActive
                  ? 'w-2.5 h-2.5 bg-brand-500'
                  : 'w-1.5 h-1.5 bg-foreground-subtle group-hover:w-2 group-hover:h-2'
              )}
            />
          </div>
        )}

        {/* Label & Description */}
        <div className="flex-1 min-w-0 py-0.5">
          <span
            className={cn(
              'block font-medium transition-colors duration-150',
              isActive
                ? 'text-foreground'
                : 'text-foreground-secondary group-hover:text-foreground'
            )}
          >
            {tab.label}
          </span>
          {tab.description && (
            <span
              className={cn(
                'block text-sm mt-0.5 transition-colors duration-150',
                isActive
                  ? 'text-foreground-muted'
                  : 'text-foreground-subtle group-hover:text-foreground-muted'
              )}
            >
              {tab.description}
            </span>
          )}
        </div>

        {/* Active indicator - vertical accent bar */}
        <div
          className={cn(
            'w-1 rounded-full transition-all duration-200 shrink-0',
            isActive
              ? 'h-8 bg-brand-500'
              : 'h-0 group-hover:h-4 group-hover:bg-brand-500/25'
          )}
        />
      </button>
    );
  };

  return (
    <div className={cn('w-full', className)}>
      {/* Mobile: Custom Dropdown (hidden on lg+) */}
      <div className="lg:hidden mb-[var(--space-heading-gap)]" ref={dropdownRef}>
        <div className="relative">
          {/* Trigger Button */}
          <button
            ref={triggerRef}
            type="button"
            onClick={() => setIsDropdownOpen(!isDropdownOpen)}
            onKeyDown={handleDropdownKeyDown}
            aria-expanded={isDropdownOpen}
            aria-haspopup="listbox"
            aria-controls={`vtab-dropdown-${generatedId}`}
            className={cn(
              'w-full flex items-center gap-3 p-4 rounded-xl',
              'border-2 border-border bg-gradient-to-b from-background to-background-secondary',
              'shadow-sm hover:shadow-md hover:border-brand-300/50 dark:hover:border-brand-700/50',
              'transition-all duration-200',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500/30 focus-visible:ring-offset-2',
              isDropdownOpen && 'shadow-md border-brand-300/50 dark:border-brand-700/50'
            )}
          >
            {ActiveIcon && (
              <ActiveIcon className="h-5 w-5 text-brand-500 shrink-0" strokeWidth={2} />
            )}
            <div className="flex-1 min-w-0 text-left">
              <span className="font-semibold text-foreground block">
                {activeTabData?.label}
              </span>
              {activeTabData?.description && (
                <span className="text-foreground-muted text-xs truncate block">
                  {activeTabData.description}
                </span>
              )}
            </div>
            <ChevronDown
              className={cn(
                'h-5 w-5 text-foreground-muted transition-transform duration-200',
                isDropdownOpen && 'rotate-180'
              )}
              strokeWidth={2}
            />
          </button>

          {/* Dropdown Panel */}
          {(isDropdownOpen || isClosing) && mobileVariant === 'dropdown' && (
            <>
              {/* Backdrop - click to close */}
              <div
                className="fixed inset-0 z-40"
                onClick={closeDropdown}
                aria-hidden="true"
              />
              {/* Panel */}
              <div
                id={`vtab-dropdown-${generatedId}`}
                role="listbox"
                aria-labelledby={`vtab-trigger-${generatedId}`}
                onKeyDown={handleDropdownKeyDown}
                className={cn(
                  'absolute z-50 w-full mt-2 p-1.5',
                  'rounded-xl border border-border bg-background/95 backdrop-blur-sm shadow-xl',
                  isClosing ? 'animate-dropdown-out' : 'animate-dropdown-in',
                  'flex flex-col gap-0.5'
                )}
              >
                {tabs.map((tab, index) => renderMobileOption(tab, index, false))}
              </div>
            </>
          )}
        </div>

        {/* Bottom Sheet */}
        {(isDropdownOpen || isClosing) && mobileVariant === 'sheet' && (
          <>
            {/* Backdrop - frosted glass effect like mobile menu */}
            <div
              className={cn(
                'fixed inset-0 z-40 bg-background/60 backdrop-blur-sm',
                isClosing ? 'animate-backdrop-out' : 'animate-backdrop'
              )}
              onClick={closeDropdown}
              aria-hidden="true"
            />
            {/* Sheet Panel */}
            <div
              id={`vtab-dropdown-${generatedId}`}
              role="listbox"
              aria-labelledby={`vtab-trigger-${generatedId}`}
              onKeyDown={handleDropdownKeyDown}
              className={cn(
                'fixed inset-x-0 bottom-0 z-50 max-h-[70vh] overflow-auto',
                'rounded-t-2xl border-t border-border bg-background shadow-2xl',
                isClosing ? 'animate-sheet-down' : 'animate-sheet-up',
                'pb-safe'
              )}
            >
              {/* Drag Handle */}
              <div className="sticky top-0 flex justify-center py-3 bg-background z-10">
                <div className="w-10 h-1 rounded-full bg-border" />
              </div>
              {/* Header */}
              <div className="px-4 pb-2">
                <h3 className="text-sm font-medium text-foreground-muted">Select Tab</h3>
              </div>
              {/* Options */}
              <div className="px-2 pb-4 flex flex-col gap-1">
                {tabs.map((tab, index) => renderMobileOption(tab, index, true))}
              </div>
            </div>
          </>
        )}
      </div>

      {/* Grid layout: stacked on mobile, side-by-side on desktop */}
      <div className="grid grid-cols-1 lg:grid-cols-12 lg:gap-[var(--space-stack-lg)]">
        {/* Desktop Tab List Sidebar (hidden on mobile) */}
        <div
          className="hidden lg:flex lg:col-span-4 flex-col gap-1"
          role="tablist"
          aria-orientation="vertical"
          aria-label="Vertical tabs"
        >
          {tabs.map((tab, index) => {
            const isActive = tab.id === activeTabId;
            const Icon = tab.icon;

            return (
              <button
                key={tab.id}
                id={`vtab-${generatedId}-${tab.id}`}
                type="button"
                role="tab"
                aria-selected={isActive}
                aria-controls={`vtab-panel-${generatedId}-${tab.id}`}
                tabIndex={isActive ? 0 : -1}
                onClick={() => handleTabChange(tab.id)}
                onKeyDown={(e) => handleDesktopKeyDown(e, index)}
                className={cn(
                  'group flex flex-col items-start rounded-md p-4 text-left transition-all',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
                  isActive
                    ? 'bg-secondary ring-border shadow-sm ring-1'
                    : 'hover:bg-background-secondary hover:pl-5'
                )}
              >
                <span
                  className={cn(
                    'font-display flex items-center gap-2 text-base font-bold',
                    isActive
                      ? 'text-brand-600 dark:text-brand-400'
                      : 'text-foreground group-hover:text-brand-600 dark:group-hover:text-brand-400'
                  )}
                >
                  {Icon && (
                    <Icon
                      className={cn(
                        'h-5 w-5',
                        isActive
                          ? 'text-brand-500'
                          : 'text-foreground-subtle group-hover:text-brand-500'
                      )}
                      strokeWidth={2}
                    />
                  )}
                  {tab.label}
                </span>
                {tab.description && (
                  <span className={cn(
                    'text-foreground-muted mt-1 text-sm',
                    Icon ? 'pl-7' : ''
                  )}>
                    {tab.description}
                  </span>
                )}
              </button>
            );
          })}
        </div>

        {/* Content Area - single render, works for both mobile and desktop */}
        <div className="col-span-1 lg:col-span-8">
          {contentPanels}
        </div>
      </div>
    </div>
  );
}

export default VerticalTabs;
