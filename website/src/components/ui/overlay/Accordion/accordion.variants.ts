import { cva, type VariantProps } from 'class-variance-authority';

export const accordionItemVariants = cva(
  'border-b border-border',
  {
    variants: {
      variant: {
        default: '',
        card: 'border rounded-lg mb-2 last:mb-0 px-4',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
);

export const accordionTriggerVariants = cva(
  [
    'flex w-full items-center justify-between py-4 text-left',
    'font-medium text-foreground',
    'transition-colors hover:text-foreground-secondary',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:rounded-sm',
  ],
  {
    variants: {
      size: {
        sm: 'text-sm py-3',
        md: 'text-sm py-4',
        lg: 'text-base py-5',
      },
    },
    defaultVariants: {
      size: 'md',
    },
  }
);

export type AccordionVariants = VariantProps<typeof accordionItemVariants>;
export type AccordionTriggerVariants = VariantProps<typeof accordionTriggerVariants>;
