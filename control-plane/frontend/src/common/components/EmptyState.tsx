type Props = {
  variant?: "block" | "inline";
  title: string;
  hint?: string;
};

export default function EmptyState({ variant = "block", title, hint }: Props) {
  if (variant === "inline") {
    return <p className="text-sm text-gray-400 italic">{title}</p>;
  }
  return (
    <div className="text-center py-16 text-gray-400">
      <p className="text-sm">{title}</p>
      {hint && <p className="text-xs mt-1">{hint}</p>}
    </div>
  );
}
