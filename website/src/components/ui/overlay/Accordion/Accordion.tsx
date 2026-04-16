import { useState, useId, type HTMLAttributes } from 'react';
import { cn } from '@/lib/cn';
import { accordionItemVariants, accordionTriggerVariants } from './accordion.variants';

interface AccordionItem {
  title: string;
  content: string;
  defaultOpen?: boolean;
}

interface AccordionProps extends HTMLAttributes<HTMLDivElement> {
  items: AccordionItem[];
  variant?: 'default' | 'card';
  size?: 'sm' | 'md' | 'lg';
  /** Only allow one item open at a time */
  exclusive?: boolean;
}

export function Accordion({
  items,
  variant = 'default',
  size = 'md',
  exclusive = false,
  className,
  ...rest
}: AccordionProps) {
  const id = useId();

  const [openItems, setOpenItems] = useState<Set<number>>(() => {
    const initial = new Set<number>();
    items.forEach((item, index) => {
      if (item.defaultOpen) initial.add(index);
    });
    return initial;
  });

  function toggle(index: number) {
    setOpenItems((prev) => {
      const next = new Set(prev);
      if (next.has(index)) {
        next.delete(index);
      } else {
        if (exclusive) next.clear();
        next.add(index);
      }
      return next;
    });
  }

  return (
    <div className={cn('w-full', className)} {...rest}>
      {items.map((item, index) => {
        const isOpen = openItems.has(index);
        const triggerId = `${id}-trigger-${index}`;
        const panelId = `${id}-panel-${index}`;

        return (
          <div key={index} className={cn(accordionItemVariants({ variant }))}>
            <button
              id={triggerId}
              type="button"
              aria-expanded={isOpen}
              aria-controls={panelId}
              className={cn(
                accordionTriggerVariants({ size }),
                'cursor-pointer',
              )}
              onClick={() => toggle(index)}
            >
              {item.title}
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                className={cn(
                  'shrink-0 text-foreground-muted transition-transform duration-200',
                  isOpen && 'rotate-180',
                )}
                aria-hidden="true"
              >
                <path d="M6 9l6 6 6-6" />
              </svg>
            </button>
            <div
              id={panelId}
              role="region"
              aria-labelledby={triggerId}
              hidden={!isOpen}
              className="pb-4 text-sm text-foreground-muted leading-relaxed"
            >
              {item.content}
            </div>
          </div>
        );
      })}
    </div>
  );
}

export default Accordion;
