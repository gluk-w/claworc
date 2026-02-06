import Editor from "@monaco-editor/react";

interface MonacoConfigEditorProps {
  value: string;
  onChange: (value: string | undefined) => void;
  height?: string;
  readOnly?: boolean;
}

export default function MonacoConfigEditor({
  value,
  onChange,
  height = "400px",
  readOnly = false,
}: MonacoConfigEditorProps) {
  return (
    <Editor
      height={height}
      defaultLanguage="json"
      value={value}
      onChange={onChange}
      options={{
        minimap: { enabled: false },
        wordWrap: "on",
        readOnly,
        fontSize: 13,
        lineNumbers: "on",
        scrollBeyondLastLine: false,
        automaticLayout: true,
        tabSize: 2,
      }}
    />
  );
}
