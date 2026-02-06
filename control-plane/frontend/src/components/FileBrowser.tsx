import { useState, useEffect, useRef } from "react";
import { Filemanager, Willow, type IApi } from "@svar-ui/react-filemanager";
import "@svar-ui/react-filemanager/all.css";
import { Upload, FilePlus, FolderPlus } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
import { useBrowseFiles, useReadFile } from "@/hooks/useFiles";
import { createFile, createDirectory, uploadFile } from "@/api/files";
import NameInputDialog from "@/components/NameInputDialog";
import type { FileEntry } from "@/types/files";

interface FileBrowserProps {
  instanceId: number;
}

interface SvarFileItem {
  id: string;
  value?: string;
  size?: number;
  date?: Date;
  type: "folder" | "file";
}

// Mapping between virtual paths and real paths
const ALLOWED_FOLDERS = [
  { virtual: "/openclaw-data", real: "/home/claworc/.openclaw", label: "openclaw-data" },
  { virtual: "/clawd-data", real: "/home/claworc/clawd", label: "clawd-data" },
];

const virtualToReal = (path: string): string => {
  if (path === "/") return "/";
  for (const folder of ALLOWED_FOLDERS) {
    if (path === folder.virtual || path.startsWith(folder.virtual + "/")) {
      return path.replace(folder.virtual, folder.real);
    }
  }
  return path;
};

const realToVirtual = (path: string): string => {
  for (const folder of ALLOWED_FOLDERS) {
    if (path === folder.real || path.startsWith(folder.real + "/")) {
      return path.replace(folder.real, folder.virtual);
    }
  }
  return path;
};

export default function FileBrowser({ instanceId }: FileBrowserProps) {
  const [currentPath, setCurrentPath] = useState("/");
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [fileData, setFileData] = useState<SvarFileItem[]>([]);
  const [isUploading, setIsUploading] = useState(false);
  const [showNewFileDialog, setShowNewFileDialog] = useState(false);
  const [showNewFolderDialog, setShowNewFolderDialog] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const apiRef = useRef<IApi | null>(null);
  const currentPathRef = useRef("/");
  const queryClient = useQueryClient();

  // Determine if we're at root or need to fetch from API
  const isRoot = currentPath === "/";
  const realPath = virtualToReal(currentPath);

  const { data: browseData, isLoading } = useBrowseFiles(
    instanceId,
    realPath,
    !isRoot, // Only fetch if not at root
  );

  // Function to invalidate the current browse query
  const refreshCurrentPath = () => {
    const pathToRefresh = virtualToReal(currentPath);
    queryClient.invalidateQueries({
      queryKey: ["instances", instanceId, "files", "browse", pathToRefresh],
    });
  };
  const { data: fileContent } = useReadFile(
    instanceId,
    selectedFile ? virtualToReal(selectedFile) : "",
    !!selectedFile,
  );

  useEffect(() => {
    if (isRoot) {
      // Show virtual root with two folders
      const rootFolders: SvarFileItem[] = ALLOWED_FOLDERS.map((folder) => ({
        id: folder.virtual,
        value: folder.label,
        type: "folder",
        size: undefined,
        date: new Date(),
      }));
      setFileData(rootFolders);
    } else if (browseData) {
      // Transform real paths to virtual paths
      const transformed: SvarFileItem[] = browseData.entries.map(
        (entry: FileEntry) => {
          const realEntryPath = `${realPath === "/" ? "" : realPath}/${entry.name}`;
          const virtualEntryPath = realToVirtual(realEntryPath);
          return {
            id: virtualEntryPath,
            value: entry.name,
            size: entry.size ? parseInt(entry.size) : undefined,
            date: new Date(),
            type: entry.type === "directory" ? "folder" : "file",
          };
        },
      );

      // SVAR builds a tree from file IDs, deriving parent from path.
      // We must include ancestor folders so SVAR can attach children properly.
      const ancestors: SvarFileItem[] = [];
      const parts = currentPath.split("/").filter(Boolean);
      for (let i = 0; i < parts.length; i++) {
        const ancestorPath = "/" + parts.slice(0, i + 1).join("/");
        ancestors.push({
          id: ancestorPath,
          value: parts[i],
          type: "folder",
          size: undefined,
          date: new Date(),
        });
      }

      setFileData([...ancestors, ...transformed]);
    }
  }, [browseData, currentPath, isRoot, realPath]);

  const handleInit = (api: IApi) => {
    apiRef.current = api;

    // Force SVAR to root on mount to match React state
    api.exec("set-path", { id: "/" });

    // Listen to set-path (runs after SVAR's internal handler) for folder navigation
    api.on("set-path", (ev: any) => {
      if (ev.id && ev.id !== currentPathRef.current) {
        currentPathRef.current = ev.id;
        setCurrentPath(ev.id);
        setSelectedFile(null);
      }
    });

    // Listen to open-file for file selection (only fires for files, not folders)
    api.on("open-file", (ev: any) => {
      const item = api.getFile(ev.id);
      if (item && item.type !== "folder") {
        setSelectedFile(item.id);
      }
    });

    // Intercept file creation to prevent SVAR's default behavior
    api.intercept("create-file", async (ev: any) => {
      if (!ev?.file?.name || !ev?.parent) {
        return false;
      }

      if (ev.parent === "/") {
        toast.error("Cannot create files at root level. Please open a folder first.");
        return false;
      }

      try {
        const virtualFilePath = `${ev.parent}/${ev.file.name}`;
        const realFilePath = virtualToReal(virtualFilePath);

        await createFile(instanceId, realFilePath, "");
        toast.success("File created successfully");

        refreshCurrentPath();
        return false;
      } catch (error: any) {
        toast.error(`Failed to create file: ${error.response?.data?.detail || error.message || "Unknown error"}`);
        return false;
      }
    });

    // Intercept file upload to handle it via our API
    api.intercept("upload-file", async (ev: any) => {
      if (!ev?.file || !ev?.parent) {
        return false;
      }

      try {
        const parentRealPath = virtualToReal(ev.parent);

        await uploadFile(instanceId, parentRealPath, ev.file);
        toast.success("File uploaded successfully");

        refreshCurrentPath();
        return false;
      } catch (error: any) {
        toast.error(`Failed to upload file: ${error.response?.data?.detail || error.message || "Unknown error"}`);
        return false;
      }
    });
  };

  const handleCreateFile = async (fileName: string) => {
    setShowNewFileDialog(false);

    try {
      const filePath = `${realPath === "/" ? "" : realPath}/${fileName}`;
      await createFile(instanceId, filePath);
      toast.success("File created successfully");
      refreshCurrentPath();
    } catch (error: any) {
      toast.error(`Failed to create file: ${error.response?.data?.detail || error.message || "Unknown error"}`);
    }
  };

  const handleCreateDirectory = async (folderName: string) => {
    setShowNewFolderDialog(false);

    try {
      const dirPath = `${realPath === "/" ? "" : realPath}/${folderName}`;
      await createDirectory(instanceId, dirPath);
      toast.success("Folder created successfully");
      refreshCurrentPath();
    } catch (error: any) {
      toast.error(`Failed to create folder: ${error.response?.data?.detail || error.message || "Unknown error"}`);
    }
  };

  const handleUploadClick = () => {
    if (isRoot) {
      toast.error("Cannot upload files to root level. Please select a folder.");
      return;
    }
    fileInputRef.current?.click();
  };

  const handleFileSelect = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files;
    if (!files || files.length === 0) return;

    const file = files[0];
    if (!file) return;

    setIsUploading(true);
    try {
      await uploadFile(instanceId, realPath, file);
      toast.success("File uploaded successfully");
      refreshCurrentPath();
      // Reset the input so the same file can be uploaded again
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    } catch (error: any) {
      toast.error(`Failed to upload file: ${error.response?.data?.detail || error.message || "Unknown error"}`);
    } finally {
      setIsUploading(false);
    }
  };

  const handleBack = () => {
    if (currentPath === "/") return;
    const parts = currentPath.split("/");
    parts.pop();
    const newPath = parts.join("/") || "/";
    currentPathRef.current = newPath;
    setCurrentPath(newPath);
    setSelectedFile(null);
    // Sync SVAR's internal navigation to match
    apiRef.current?.exec("set-path", { id: newPath });
  };

  if (isLoading && fileData.length === 0) {
    return (
      <div className="flex items-center justify-center h-96 text-gray-500">
        Loading files...
      </div>
    );
  }

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <button
          onClick={handleBack}
          disabled={currentPath === "/"}
          className="px-3 py-1 text-sm bg-gray-100 rounded hover:bg-gray-200 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          ← Back
        </button>
        <span className="text-sm text-gray-600 flex-1">
          Path: {currentPath === "/" ? "Root" : currentPath}
        </span>
        <button
          onClick={() => setShowNewFileDialog(true)}
          disabled={isRoot}
          className="px-3 py-1 text-sm bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1"
          title="Create new file"
        >
          <FilePlus size={14} />
          New File
        </button>
        <button
          onClick={() => setShowNewFolderDialog(true)}
          disabled={isRoot}
          className="px-3 py-1 text-sm bg-indigo-600 text-white rounded hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1"
          title="Create new folder"
        >
          <FolderPlus size={14} />
          New Folder
        </button>
        <button
          onClick={handleUploadClick}
          disabled={isRoot || isUploading}
          className="px-3 py-1 text-sm bg-green-600 text-white rounded hover:bg-green-700 disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1"
          title="Upload file"
        >
          <Upload size={14} />
          {isUploading ? "Uploading..." : "Upload"}
        </button>
        <input
          ref={fileInputRef}
          type="file"
          onChange={handleFileSelect}
          className="hidden"
        />
      </div>

      <div className="flex gap-4 h-[600px]">
        <div className="flex-1 border border-gray-200 rounded-lg overflow-hidden">
          <Willow>
            <Filemanager data={fileData} init={handleInit} />
          </Willow>
        </div>
        {selectedFile && fileContent && (
          <div className="w-1/2 border border-gray-200 rounded-lg overflow-hidden bg-white">
            <div className="border-b border-gray-200 px-4 py-2 bg-gray-50 flex items-center justify-between">
              <h3 className="text-sm font-medium text-gray-900">
                {selectedFile.split("/").pop()}
              </h3>
              <button
                onClick={() => setSelectedFile(null)}
                className="text-gray-500 hover:text-gray-700"
              >
                Close
              </button>
            </div>
            <div className="p-4 overflow-auto h-[calc(100%-48px)]">
              <pre className="text-xs text-gray-800 whitespace-pre-wrap break-words font-mono">
                {fileContent.content}
              </pre>
            </div>
          </div>
        )}
      </div>

      {showNewFileDialog && (
        <NameInputDialog
          title="Create New File"
          placeholder="Enter file name"
          onConfirm={handleCreateFile}
          onCancel={() => setShowNewFileDialog(false)}
        />
      )}

      {showNewFolderDialog && (
        <NameInputDialog
          title="Create New Folder"
          placeholder="Enter folder name"
          onConfirm={handleCreateDirectory}
          onCancel={() => setShowNewFolderDialog(false)}
        />
      )}
    </div>
  );
}
