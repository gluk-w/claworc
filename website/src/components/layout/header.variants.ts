import { cva, type VariantProps } from 'class-variance-authority';

export const headerVariants = cva('z-50', {
  variants: {
    position: {
      fixed: 'fixed top-0 left-0 right-0',
      sticky: 'sticky top-0',
      static: 'relative',
    },
    variant: {
      default: 'bg-background/80 backdrop-blur-lg border-b border-border/50',
      solid: 'bg-background border-b border-border-strong',
      transparent: 'bg-transparent',
    },
    shape: {
      bar: 'w-full transition-[background,border-color,box-shadow,backdrop-filter] duration-300',
      floating: 'rounded-2xl transition-[background,border-color,box-shadow] duration-300',
    },
  },
  compoundVariants: [
    // Floating + fixed: centered with gap
    { shape: 'floating', position: 'fixed', class: '!left-1/2 !right-auto -translate-x-1/2 w-[calc(100%-2rem)] max-w-6xl mt-4' },
    // Floating + sticky: centered with gap
    { shape: 'floating', position: 'sticky', class: '!top-4 mx-auto max-w-6xl' },
    // Floating + static: centered
    { shape: 'floating', position: 'static', class: 'mx-auto max-w-6xl' },
    // Floating + transparent: glass effect
    { shape: 'floating', variant: 'transparent', class: 'bg-white/[0.06] backdrop-blur-xl border border-white/[0.08]' },
    // Floating + default: semi-transparent with blur
    { shape: 'floating', variant: 'default', class: '!bg-background/80 backdrop-blur-xl !border border-border/50 !border-b-border/50' },
    // Floating + solid: opaque
    { shape: 'floating', variant: 'solid', class: '!bg-background !border border-border !border-b-border' },
  ],
  defaultVariants: {
    position: 'sticky',
    variant: 'default',
    shape: 'bar',
  },
});

export const headerInnerVariants = cva(
  'flex items-center justify-between md:grid md:grid-cols-[1fr_auto_1fr]',
  {
    variants: {
      size: {
        sm: 'h-12',
        md: 'h-14',
        lg: 'h-16',
      },
      shape: {
        bar: 'mx-auto max-w-6xl px-6',
        floating: 'px-4',
      },
    },
    defaultVariants: {
      size: 'md',
      shape: 'bar',
    },
  }
);

export type HeaderVariants = VariantProps<typeof headerVariants>;
export type HeaderInnerVariants = VariantProps<typeof headerInnerVariants>;
