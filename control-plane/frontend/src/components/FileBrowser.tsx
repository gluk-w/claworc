import { useState, useEffect, useRef, useCallback } from "react";
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
  initialPath?: string;
  onPathChange?: (path: string) => void;
}

interface SvarFileItem {
  id: string;
  value?: string;
  size?: number;
  date?: Date;
  type: "folder" | "file";
}

const ROOT_PATH = "/home/claworc";

export default function FileBrowser({ instanceId, initialPath = "/", onPathChange }: FileBrowserProps) {
  const [currentPath, setCurrentPath] = useState(initialPath);
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [fileData, setFileData] = useState<SvarFileItem[]>([]);
  const [isUploading, setIsUploading] = useState(false);
  const [showNewFileDialog, setShowNewFileDialog] = useState(false);
  const [showNewFolderDialog, setShowNewFolderDialog] = useState(false);
  const [editedContent, setEditedContent] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const apiRef = useRef<IApi | null>(null);
  const currentPathRef = useRef(initialPath);
  // Cache of virtualPath -> SvarFileItems for that directory, so the sidebar tree stays expanded
  const dirCacheRef = useRef<Map<string, SvarFileItem[]>>(new Map());
  const onPathChangeRef = useRef(onPathChange);
  onPathChangeRef.current = onPathChange;
  const queryClient = useQueryClient();

  const updatePath = useCallback((newPath: string) => {
    currentPathRef.current = newPath;
    setCurrentPath(newPath);
    onPathChangeRef.current?.(newPath);
  }, []);

  // The real filesystem path to browse
  const realPath = currentPath === "/" ? ROOT_PATH : ROOT_PATH + currentPath;

  const { data: browseData, isLoading } = useBrowseFiles(
    instanceId,
    realPath,
    true,
  );

  // Function to invalidate the current browse query
  const refreshCurrentPath = () => {
    queryClient.invalidateQueries({
      queryKey: ["instances", instanceId, "files", "browse", realPath],
    });
  };
  const { data: fileContent } = useReadFile(
    instanceId,
    selectedFile ? (selectedFile === "/" ? ROOT_PATH : ROOT_PATH + selectedFile) : "",
    !!selectedFile,
  );

  useEffect(() => {
    if (browseData) {
      // Transform API response into SVAR items for this directory
      const transformed: SvarFileItem[] = browseData.entries.map(
        (entry: FileEntry) => {
          const virtualEntryPath = `${currentPath === "/" ? "" : currentPath}/${entry.name}`;
          return {
            id: virtualEntryPath,
            value: entry.name,
            size: entry.size ? parseInt(entry.size) : undefined,
            date: new Date(),
            type: entry.type === "directory" ? "folder" : "file",
          };
        },
      );

      // Cache this directory's contents so the tree stays expanded on navigation
      dirCacheRef.current.set(currentPath, transformed);

      // Build fileData from all cached directories, deduplicating by id
      const seen = new Set<string>();
      const allItems: SvarFileItem[] = [];

      for (const items of dirCacheRef.current.values()) {
        for (const item of items) {
          if (!seen.has(item.id)) {
            seen.add(item.id);
            allItems.push(item);
          }
        }
      }

      // SVAR builds a tree from file IDs, deriving parent from path.
      // We must include ancestor folders so SVAR can attach children properly.
      const parts = currentPath.split("/").filter(Boolean);
      for (let i = 0; i < parts.length; i++) {
        const ancestorPath = "/" + parts.slice(0, i + 1).join("/");
        if (!seen.has(ancestorPath)) {
          seen.add(ancestorPath);
          allItems.push({
            id: ancestorPath,
            value: parts[i],
            type: "folder",
            size: undefined,
            date: new Date(),
          });
        }
      }

      setFileData(allItems);
    }
  }, [browseData, currentPath]);

  // After data loads, sync SVAR's internal path to match React state.
  // On reload, handleInit fires before data exists so set-path fails silently;
  // this effect re-applies it once the tree is populated.
  useEffect(() => {
    if (apiRef.current && fileData.length > 0 && currentPathRef.current !== "/") {
      apiRef.current.exec("set-path", { id: currentPathRef.current });
    }
  }, [fileData]);

  const handleInit = (api: IApi) => {
    apiRef.current = api;

    // Force SVAR to match React state on mount
    api.exec("set-path", { id: currentPathRef.current });

    // Listen to set-path (runs after SVAR's internal handler) for folder navigation
    api.on("set-path", (ev: any) => {
      if (ev.id && ev.id !== currentPathRef.current) {
        updatePath(ev.id);
        setSelectedFile(null);
        setEditedContent(null);
      }
    });

    // Listen to open-file for file selection (only fires for files, not folders)
    api.on("open-file", (ev: any) => {
      const item = api.getFile(ev.id);
      if (item && item.type !== "folder") {
        setSelectedFile(item.id);
        setEditedContent(null);
      }
    });

    // Intercept file creation to prevent SVAR's default behavior
    api.intercept("create-file", async (ev: any) => {
      if (!ev?.file?.name || !ev?.parent) {
        return false;
      }

      try {
        const filePath = `${ev.parent === "/" ? ROOT_PATH : ROOT_PATH + ev.parent}/${ev.file.name}`;

        await createFile(instanceId, filePath, "");
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
        const parentRealPath = ev.parent === "/" ? ROOT_PATH : ROOT_PATH + ev.parent;

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
      const filePath = `${realPath}/${fileName}`;
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
      const dirPath = `${realPath}/${folderName}`;
      await createDirectory(instanceId, dirPath);
      toast.success("Folder created successfully");
      refreshCurrentPath();
    } catch (error: any) {
      toast.error(`Failed to create folder: ${error.response?.data?.detail || error.message || "Unknown error"}`);
    }
  };

  const handleUploadClick = () => {
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

  const handleSaveFile = async () => {
    if (!selectedFile || editedContent === null) return;
    setIsSaving(true);
    try {
      const filePath = selectedFile === "/" ? ROOT_PATH : ROOT_PATH + selectedFile;
      await createFile(instanceId, filePath, editedContent);
      toast.success("File saved");
      setEditedContent(null);
      // Invalidate the read cache so re-opening shows fresh content
      queryClient.invalidateQueries({
        queryKey: ["instances", instanceId, "files", "read"],
      });
    } catch (error: any) {
      toast.error(`Failed to save: ${error.response?.data?.detail || error.message || "Unknown error"}`);
    } finally {
      setIsSaving(false);
    }
  };

  const handleCloseEditor = () => {
    setSelectedFile(null);
    setEditedContent(null);
  };

  const handleBack = () => {
    if (currentPath === "/") return;
    const parts = currentPath.split("/");
    parts.pop();
    const newPath = parts.join("/") || "/";
    updatePath(newPath);
    setSelectedFile(null);
    setEditedContent(null);
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
    <div className="h-full flex flex-col">
      <div className="mb-4 flex items-center gap-3 shrink-0">
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
          className="px-3 py-1 text-sm bg-blue-600 text-white rounded hover:bg-blue-700 flex items-center gap-1"
          title="Create new file"
        >
          <FilePlus size={14} />
          New File
        </button>
        <button
          onClick={() => setShowNewFolderDialog(true)}
          className="px-3 py-1 text-sm bg-indigo-600 text-white rounded hover:bg-indigo-700 flex items-center gap-1"
          title="Create new folder"
        >
          <FolderPlus size={14} />
          New Folder
        </button>
        <button
          onClick={handleUploadClick}
          disabled={isUploading}
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

      <div className="flex gap-4 flex-1 min-h-0">
        <div className="flex-1 border border-gray-200 rounded-lg overflow-hidden">
          <Willow>
            <Filemanager data={fileData} init={handleInit} />
          </Willow>
        </div>
        {selectedFile && fileContent && (
          <div className="w-1/2 border border-gray-200 rounded-lg overflow-hidden bg-white flex flex-col">
            <div className="border-b border-gray-200 px-4 py-2 bg-gray-50 flex items-center justify-between shrink-0">
              <h3 className="text-sm font-medium text-gray-900">
                {selectedFile.split("/").pop()}
                {editedContent !== null && <span className="ml-1 text-amber-600">*</span>}
              </h3>
              <div className="flex items-center gap-2">
                {editedContent !== null && (
                  <>
                    <button
                      onClick={handleSaveFile}
                      disabled={isSaving}
                      className="text-xs px-2 py-1 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
                    >
                      {isSaving ? "Saving..." : "Save"}
                    </button>
                    <button
                      onClick={() => setEditedContent(null)}
                      className="text-xs px-2 py-1 text-gray-600 hover:text-gray-800"
                    >
                      Discard
                    </button>
                  </>
                )}
                <button
                  onClick={handleCloseEditor}
                  className="text-gray-500 hover:text-gray-700"
                >
                  Close
                </button>
              </div>
            </div>
            <textarea
              className="flex-1 w-full p-4 text-xs text-gray-800 font-mono resize-none outline-none"
              value={editedContent ?? fileContent.content}
              onChange={(e) => setEditedContent(e.target.value)}
              spellCheck={false}
            />
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
