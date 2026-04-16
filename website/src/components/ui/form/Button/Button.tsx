import { type ButtonHTMLAttributes, type AnchorHTMLAttributes, type Ref } from 'react';
import { cn } from '@/lib/cn';
import { isExternalUrl } from '@/lib/utils';
import { buttonVariants, type ButtonVariants } from './button.variants';

interface BaseProps {
  ref?: Ref<HTMLButtonElement | HTMLAnchorElement>;
  variant?: ButtonVariants['variant'];
  size?: ButtonVariants['size'];
  loading?: boolean;
  fullWidth?: boolean;
  icon?: boolean;
  disabled?: boolean;
}

type ButtonAsButton = BaseProps &
  Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'ref'> & {
    href?: never;
  };

type ButtonAsLink = BaseProps &
  Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'ref'> & {
    href: string;
  };

type ButtonProps = ButtonAsButton | ButtonAsLink;

const LoadingSpinner = () => (
  <svg
    className="animate-spin"
    xmlns="http://www.w3.org/2000/svg"
    fill="none"
    viewBox="0 0 24 24"
    aria-hidden="true"
  >
    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
    <path
      className="opacity-75"
      fill="currentColor"
      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
    />
  </svg>
);

export function Button(props: ButtonProps) {
  const {
    ref,
    variant = 'primary',
    size = 'md',
    loading = false,
    fullWidth = false,
    icon = false,
    className,
    children,
    disabled,
    ...rest
  } = props;

  const classes = cn(buttonVariants({ variant, size, fullWidth, icon }), className);

  if ('href' in props && props.href) {
    const isExternal = isExternalUrl(props.href);
    const linkProps = rest as AnchorHTMLAttributes<HTMLAnchorElement>;

    return (
      <a
        ref={ref as Ref<HTMLAnchorElement>}
        className={classes}
        target={linkProps.target ?? (isExternal ? '_blank' : undefined)}
        rel={isExternal ? 'noopener noreferrer' : linkProps.rel}
        {...linkProps}
      >
        {loading && <LoadingSpinner />}
        {children}
      </a>
    );
  }

  return (
    <button
      ref={ref as Ref<HTMLButtonElement>}
      className={classes}
      disabled={disabled || loading}
      {...(rest as ButtonHTMLAttributes<HTMLButtonElement>)}
    >
      {loading && <LoadingSpinner />}
      {children}
    </button>
  );
}

export default Button;
