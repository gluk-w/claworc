import { type HTMLAttributes, type Ref, useState } from 'react';
import { cn } from '@/lib/cn';
import { avatarVariants, type AvatarVariants } from './avatar.variants';

interface AvatarProps extends Omit<HTMLAttributes<HTMLDivElement>, 'ref'> {
  ref?: Ref<HTMLDivElement>;
  src?: string;
  alt?: string;
  fallback?: string;
  size?: AvatarVariants['size'];
}

export function Avatar({ ref, src, alt = '', fallback, size = 'md', className, ...rest }: AvatarProps) {
  const [imgError, setImgError] = useState(false);

  const initials = fallback || alt
    .split(' ')
    .map((word) => word[0])
    .join('')
    .slice(0, 2)
    .toUpperCase();

  return (
    <div ref={ref} className={cn(avatarVariants({ size }), className)} {...rest}>
      {src && !imgError ? (
        <>
          <img
            src={src}
            alt={alt}
            className="w-full h-full object-cover"
            loading="lazy"
            decoding="async"
            onError={() => setImgError(true)}
          />
          <span className="sr-only">{alt || 'User avatar'}</span>
        </>
      ) : (
        <>
          <span aria-hidden="true">{initials || '?'}</span>
          <span className="sr-only">{alt || 'User avatar'}</span>
        </>
      )}
    </div>
  );
}

export default Avatar;
