import Editor from "@monaco-editor/react";

interface MonacoConfigEditorProps {
  value: string;
  onChange: (value: string | undefined) => void;
  height?: string;
  readOnly?: boolean;
  language?: string;
  path?: string;
}

export default function MonacoConfigEditor({
  value,
  onChange,
  height = "400px",
  readOnly = false,
  language = "json",
  path,
}: MonacoConfigEditorProps) {
  return (
    <Editor
      height={height}
      language={language}
      path={path}
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
