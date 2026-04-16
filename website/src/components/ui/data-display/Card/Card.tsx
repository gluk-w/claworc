import { type HTMLAttributes, type Ref, type ReactNode } from 'react';
import { cn } from '@/lib/cn';
import { cardVariants, type CardVariants } from './card.variants';

type CardShadow = 'none' | 'sm' | 'md' | 'lg';

interface CardProps extends Omit<HTMLAttributes<HTMLDivElement>, 'ref'> {
  ref?: Ref<HTMLDivElement>;
  padding?: CardVariants['padding'];
  shadow?: CardShadow;
  hover?: boolean;
  /** Visual style variant */
  variant?: CardVariants['variant'];
  /** Icon element to display in the card header */
  icon?: ReactNode;
  /** Card title */
  title?: string;
  /** Card subtitle/byline */
  subtitle?: string;
  /** Card description */
  description?: string;
  /** Whether to use the structured layout with icon/title/description */
  structured?: boolean;
}

const shadows: Record<CardShadow, string> = {
  none: '',
  sm: 'shadow-sm',
  md: 'shadow-md',
  lg: 'shadow-lg',
};

export function Card({
  ref,
  padding = 'md',
  shadow = 'none',
  hover = false,
  variant = 'default',
  icon,
  title,
  subtitle,
  description,
  structured = false,
  className,
  children,
  ...props
}: CardProps) {
  const cardStyles = cn(
    cardVariants({ variant, padding, hover }),
    shadows[shadow],
    className
  );

  // If using structured layout with icon/title/description
  if (structured || icon || title) {
    return (
      <div ref={ref} className={cardStyles} {...props}>
        <div className="flex items-start gap-4">
          {icon && (
            <div className="w-11 h-11 rounded-xl bg-gradient-to-br from-brand-500/20 to-brand-500/5 flex items-center justify-center text-brand-500 shrink-0">
              {icon}
            </div>
          )}
          {(title || subtitle) && (
            <div className="flex-1 min-w-0">
              {title && (
                <h3 className="text-base font-semibold text-foreground">{title}</h3>
              )}
              {subtitle && (
                <p className="text-xs text-foreground-subtle mt-0.5 font-medium">{subtitle}</p>
              )}
            </div>
          )}
        </div>
        {description && (
          <div className="mt-4">
            <p className="text-sm text-foreground-muted leading-relaxed">{description}</p>
          </div>
        )}
        {children}
      </div>
    );
  }

  return (
    <div ref={ref} className={cardStyles} {...props}>
      {children}
    </div>
  );
}

// Card sub-components with refined spacing
interface CardSubComponentProps extends Omit<HTMLAttributes<HTMLDivElement>, 'ref'> {
  ref?: Ref<HTMLDivElement>;
}

interface CardTitleProps extends Omit<HTMLAttributes<HTMLHeadingElement>, 'ref'> {
  ref?: Ref<HTMLHeadingElement>;
}

interface CardTextProps extends Omit<HTMLAttributes<HTMLParagraphElement>, 'ref'> {
  ref?: Ref<HTMLParagraphElement>;
}

export function CardHeader({ ref, className, ...props }: CardSubComponentProps) {
  return <div ref={ref} className={cn('flex flex-col gap-1', className)} {...props} />;
}

export function CardTitle({ ref, className, ...props }: CardTitleProps) {
  return (
    <h3
      ref={ref}
      className={cn(
        'text-base font-black leading-tight tracking-tight text-foreground',
        className
      )}
      {...props}
    />
  );
}

export function CardByline({ ref, className, ...props }: CardTextProps) {
  return (
    <p
      ref={ref}
      className={cn('text-xs text-foreground-subtle mt-0.5 font-medium', className)}
      {...props}
    />
  );
}

export function CardDescription({ ref, className, ...props }: CardTextProps) {
  return (
    <p
      ref={ref}
      className={cn('text-sm text-foreground-muted leading-relaxed mt-1.5', className)}
      {...props}
    />
  );
}

export function CardContent({ ref, className, ...props }: CardSubComponentProps) {
  return <div ref={ref} className={cn('mt-4', className)} {...props} />;
}

export function CardFooter({ ref, className, ...props }: CardSubComponentProps) {
  return (
    <div
      ref={ref}
      className={cn('flex items-center mt-4 pt-4 border-t border-border', className)}
      {...props}
    />
  );
}

export default Card;
