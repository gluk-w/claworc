import {
  useState,
  useRef,
  useEffect,
  useCallback,
  type ReactNode,
} from 'react';
import { cn } from '@/lib/cn';
import { Icon } from '@/components/ui/primitives/Icon/Icon';

interface DropdownItem {
  label: string;
  href?: string;
  icon?: string;
  disabled?: boolean;
  separator?: boolean;
  onClick?: () => void;
}

interface DropdownProps {
  items: DropdownItem[];
  align?: 'start' | 'end';
  trigger: ReactNode;
  className?: string;
}

// Map common action names to resolved icon names
const iconNameMap: Record<string, string> = {
  edit: 'edit',
  copy: 'copy',
  share: 'share',
  archive: 'archive',
  trash: 'trash',
  delete: 'trash',
  download: 'download',
  upload: 'upload',
  settings: 'settings',
  view: 'external-link',
};

export function Dropdown({ items, align = 'start', trigger, className }: DropdownProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [focusedIndex, setFocusedIndex] = useState(-1);
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const itemRefs = useRef<(HTMLElement | null)[]>([]);

  // Build a list of actionable (non-separator) item indices
  const actionableIndices = items.reduce<number[]>((acc, item, i) => {
    if (!item.separator && !item.disabled) acc.push(i);
    return acc;
  }, []);

  const open = useCallback(() => {
    setIsOpen(true);
    // Focus first actionable item
    const firstActionable = actionableIndices[0];
    if (firstActionable !== undefined) {
      setFocusedIndex(firstActionable);
      requestAnimationFrame(() => {
        itemRefs.current[firstActionable]?.focus();
      });
    }
  }, [actionableIndices]);

  const close = useCallback(() => {
    setIsOpen(false);
    setFocusedIndex(-1);
  }, []);

  const focusItem = useCallback((index: number) => {
    // Cycle through actionable indices
    const pos = actionableIndices.indexOf(index);
    let targetIndex: number;

    if (pos !== -1) {
      targetIndex = index;
    } else if (index < 0 || index < actionableIndices[0]) {
      targetIndex = actionableIndices[actionableIndices.length - 1];
    } else {
      targetIndex = actionableIndices[0];
    }

    setFocusedIndex(targetIndex);
    requestAnimationFrame(() => {
      itemRefs.current[targetIndex]?.focus();
    });
  }, [actionableIndices]);

  const focusNext = useCallback(() => {
    const currentPos = actionableIndices.indexOf(focusedIndex);
    const nextPos = currentPos + 1 >= actionableIndices.length ? 0 : currentPos + 1;
    const nextIndex = actionableIndices[nextPos];
    setFocusedIndex(nextIndex);
    requestAnimationFrame(() => {
      itemRefs.current[nextIndex]?.focus();
    });
  }, [actionableIndices, focusedIndex]);

  const focusPrev = useCallback(() => {
    const currentPos = actionableIndices.indexOf(focusedIndex);
    const prevPos = currentPos - 1 < 0 ? actionableIndices.length - 1 : currentPos - 1;
    const prevIndex = actionableIndices[prevPos];
    setFocusedIndex(prevIndex);
    requestAnimationFrame(() => {
      itemRefs.current[prevIndex]?.focus();
    });
  }, [actionableIndices, focusedIndex]);

  // Click outside detection
  useEffect(() => {
    function handleMouseDown(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        close();
      }
    }

    document.addEventListener('mousedown', handleMouseDown);
    return () => document.removeEventListener('mousedown', handleMouseDown);
  }, [close]);

  function handleTriggerClick(e: React.MouseEvent) {
    e.stopPropagation();
    if (isOpen) {
      close();
    } else {
      open();
    }
  }

  function handleTriggerKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
      e.preventDefault();
      open();
    }
  }

  function handleMenuKeyDown(e: React.KeyboardEvent) {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        focusNext();
        break;
      case 'ArrowUp':
        e.preventDefault();
        focusPrev();
        break;
      case 'Home':
        e.preventDefault();
        if (actionableIndices.length > 0) {
          focusItem(actionableIndices[0]);
        }
        break;
      case 'End':
        e.preventDefault();
        if (actionableIndices.length > 0) {
          focusItem(actionableIndices[actionableIndices.length - 1]);
        }
        break;
      case 'Escape':
        e.preventDefault();
        close();
        // Return focus to trigger
        requestAnimationFrame(() => {
          const triggerEl = triggerRef.current?.querySelector<HTMLElement>(
            'button, [tabindex], a'
          ) || triggerRef.current;
          triggerEl?.focus();
        });
        break;
      case 'Tab':
        close();
        break;
    }
  }

  function handleItemActivate(item: DropdownItem) {
    if (item.disabled) return;
    if (item.onClick) {
      item.onClick();
      close();
    }
    // If href, let the link navigate naturally
    if (!item.href) {
      close();
    }
  }

  function handleItemKeyDown(e: React.KeyboardEvent, item: DropdownItem) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleItemActivate(item);
    }
  }

  return (
    <div ref={containerRef} className={cn('relative inline-block', className)}>
      <div
        ref={triggerRef}
        onClick={handleTriggerClick}
        onKeyDown={handleTriggerKeyDown}
      >
        {trigger}
      </div>

      <div
        ref={menuRef}
        className={cn(
          'absolute z-[100] mt-2 min-w-[180px] p-1.5',
          'rounded-xl bg-background border border-border shadow-xl',
          'transition-all duration-150 ease-out',
          isOpen
            ? 'opacity-100 visible translate-y-0'
            : 'opacity-0 invisible translate-y-1',
          align === 'end' ? 'right-0' : 'left-0'
        )}
        role="menu"
        aria-orientation="vertical"
        onKeyDown={handleMenuKeyDown}
      >
        {items.map((item, index) =>
          item.separator ? (
            <div key={index} className="my-1.5 h-px bg-border" role="separator" />
          ) : item.href ? (
            <a
              key={index}
              ref={(el) => { itemRefs.current[index] = el; }}
              href={item.href}
              className={cn(
                'flex items-center gap-2.5 px-3 py-2 text-sm rounded-md',
                'transition-colors outline-none',
                'focus-visible:ring-2 focus-visible:ring-ring',
                item.disabled
                  ? 'opacity-50 cursor-not-allowed pointer-events-none text-foreground-muted'
                  : 'hover:bg-secondary focus:bg-secondary text-foreground-secondary hover:text-foreground'
              )}
              role="menuitem"
              tabIndex={item.disabled ? -1 : 0}
              aria-disabled={item.disabled || undefined}
              onClick={(e) => {
                if (item.disabled) {
                  e.preventDefault();
                  return;
                }
                handleItemActivate(item);
              }}
              onKeyDown={(e) => handleItemKeyDown(e, item)}
            >
              {item.icon && <Icon name={iconNameMap[item.icon] ?? item.icon} size="sm" className={item.disabled ? 'text-foreground-subtle' : 'text-foreground-muted'} />}
              <span>{item.label}</span>
            </a>
          ) : (
            <button
              key={index}
              ref={(el) => { itemRefs.current[index] = el; }}
              type="button"
              className={cn(
                'flex w-full items-center gap-2.5 px-3 py-2 text-sm rounded-md',
                'transition-colors outline-none',
                'focus-visible:ring-2 focus-visible:ring-ring',
                item.disabled
                  ? 'opacity-50 cursor-not-allowed pointer-events-none text-foreground-muted'
                  : 'hover:bg-secondary focus:bg-secondary text-foreground-secondary hover:text-foreground'
              )}
              role="menuitem"
              tabIndex={item.disabled ? -1 : 0}
              aria-disabled={item.disabled || undefined}
              onClick={() => handleItemActivate(item)}
              onKeyDown={(e) => handleItemKeyDown(e, item)}
            >
              {item.icon && <Icon name={iconNameMap[item.icon] ?? item.icon} size="sm" className={item.disabled ? 'text-foreground-subtle' : 'text-foreground-muted'} />}
              <span>{item.label}</span>
            </button>
          )
        )}
      </div>
    </div>
  );
}

export default Dropdown;
