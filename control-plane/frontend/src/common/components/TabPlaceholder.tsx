interface TabPlaceholderProps {
  message: string;
}

export function TabPlaceholder({ message }: TabPlaceholderProps) {
  return (
    <div className="flex items-center justify-center h-full text-gray-500 text-sm bg-white rounded-lg border border-gray-200">
      {message}
    </div>
  );
}
