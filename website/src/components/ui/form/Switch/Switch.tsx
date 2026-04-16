import { type Ref, useId, useState } from 'react';
import { cn } from '@/lib/cn';
import { switchTrackVariants, switchThumbVariants } from './switch.variants';

interface SwitchProps {
  ref?: Ref<HTMLButtonElement>;
  label?: string;
  description?: string;
  size?: 'sm' | 'md' | 'lg';
  checked?: boolean;
  defaultChecked?: boolean;
  disabled?: boolean;
  name?: string;
  value?: string;
  onCheckedChange?: (checked: boolean) => void;
  className?: string;
  id?: string;
}

export function Switch({
  ref,
  label,
  description,
  size = 'md',
  checked: controlledChecked,
  defaultChecked = false,
  disabled = false,
  name,
  value,
  onCheckedChange,
  className,
  id,
}: SwitchProps) {
  const generatedId = useId();
  const switchId = id || generatedId;
  const [internalChecked, setInternalChecked] = useState(defaultChecked);

  const isControlled = controlledChecked !== undefined;
  const isChecked = isControlled ? controlledChecked : internalChecked;

  const handleClick = () => {
    if (disabled) return;
    const newValue = !isChecked;
    if (!isControlled) {
      setInternalChecked(newValue);
    }
    onCheckedChange?.(newValue);
  };

  return (
    <div className={cn('flex items-start gap-3', className)}>
      <button
        ref={ref}
        type="button"
        role="switch"
        id={switchId}
        aria-checked={isChecked}
        aria-describedby={description ? `${switchId}-description` : undefined}
        disabled={disabled}
        onClick={handleClick}
        className={cn(switchTrackVariants({ size, checked: isChecked }))}
      >
        <span className={cn(switchThumbVariants({ size, checked: isChecked }))} />
      </button>

      {/* Hidden input for form submission */}
      {name && (
        <input type="hidden" name={name} value={isChecked ? (value || 'on') : ''} />
      )}

      {(label || description) && (
        <div className="grid gap-0.5">
          {label && (
            <label
              htmlFor={switchId}
              className={cn('text-sm font-medium text-foreground cursor-pointer', disabled && 'cursor-not-allowed')}
            >
              {label}
            </label>
          )}
          {description && (
            <p id={`${switchId}-description`} className="text-xs text-foreground-subtle leading-normal">{description}</p>
          )}
        </div>
      )}
    </div>
  );
}

export default Switch;
