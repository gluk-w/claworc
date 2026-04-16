import { type HTMLAttributes, type Ref } from 'react';
import { cn } from '@/lib/cn';
import { Avatar } from '../Avatar/Avatar';
import type { AvatarVariants } from '../Avatar/avatar.variants';

interface AvatarItem {
  src?: string;
  alt?: string;
  fallback?: string;
}

interface AvatarGroupProps extends Omit<HTMLAttributes<HTMLDivElement>, 'ref'> {
  ref?: Ref<HTMLDivElement>;
  avatars: AvatarItem[];
  max?: number;
  size?: NonNullable<AvatarVariants['size']>;
}

const overflowSizes: Record<string, string> = {
  xs: 'w-6 h-6 text-[8px]',
  sm: 'w-8 h-8 text-[10px]',
  md: 'w-10 h-10 text-xs',
  lg: 'w-12 h-12 text-sm',
  xl: 'w-14 h-14 text-base',
};

export function AvatarGroup({ ref, avatars, max = 4, size = 'md', className, ...rest }: AvatarGroupProps) {
  const visibleAvatars = avatars.slice(0, max);
  const overflowCount = Math.max(0, avatars.length - max);

  return (
    <div ref={ref} className={cn('flex -space-x-2', className)} {...rest}>
      {visibleAvatars.map((avatar, i) => (
        <Avatar
          key={i}
          src={avatar.src}
          alt={avatar.alt || ''}
          fallback={avatar.fallback}
          size={size}
          className="ring-2 ring-background"
        />
      ))}
      {overflowCount > 0 && (
        <div
          className={cn(
            'relative inline-flex items-center justify-center',
            'rounded-full overflow-hidden',
            'bg-secondary text-foreground-muted font-semibold',
            'ring-2 ring-background',
            overflowSizes[size]
          )}
          aria-label={`${overflowCount} more`}
        >
          +{overflowCount}
        </div>
      )}
    </div>
  );
}

export default AvatarGroup;
