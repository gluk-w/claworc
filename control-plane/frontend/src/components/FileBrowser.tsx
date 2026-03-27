import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import { Filemanager, Willow, type IApi } from "@svar-ui/react-filemanager";
import "@svar-ui/react-filemanager/all.css";
import { useQueryClient } from "@tanstack/react-query";
import { successToast, errorToast } from "@/utils/toast";
import { useBrowseFiles, useReadFile } from "@/hooks/useFiles";
import { createFile, createDirectory, uploadFile, deleteFile, renameFile } from "@/api/files";
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
  const [editedContent, setEditedContent] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
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

  // Declarative panel path: SVAR preserves path when the reference is stable
  // (i.e. only data changed, not the path). This keeps the sidebar selection in sync.
  const panels = useMemo(() => [{ path: currentPath }], [currentPath]);

  const { data: browseData } = useBrowseFiles(
    instanceId,
    realPath,
    true,
  );

  // Stable ref so interceptors (captured at mount) always refresh the *current* path
  const refreshCurrentPathRef = useRef<() => void>(() => {});
  refreshCurrentPathRef.current = () => {
    const p = currentPathRef.current;
    const rp = p === "/" ? ROOT_PATH : ROOT_PATH + p;
    queryClient.invalidateQueries({
      queryKey: ["instances", instanceId, "files", "browse", rp],
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
      const transformed: SvarFileItem[] = (browseData.entries ?? []).map(
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

  // After SVAR rebuilds its tree (on data change), re-expand all previously
  // visited folders and ancestors of the current path in the sidebar.
  // SVAR's DataStore resets all nodes to closed on rebuild; only root gets open:true.
  useEffect(() => {
    if (!apiRef.current || fileData.length === 0) return;
    const toExpand = new Set<string>();
    // All previously browsed directories
    for (const path of dirCacheRef.current.keys()) {
      if (path !== "/") toExpand.add(path);
    }
    // Ancestors of current path
    const parts = currentPathRef.current.split("/").filter(Boolean);
    for (let i = 0; i < parts.length; i++) {
      toExpand.add("/" + parts.slice(0, i + 1).join("/"));
    }
    for (const path of toExpand) {
      apiRef.current.exec("open-tree-folder", { id: path, mode: true });
    }
  }, [fileData]);

  const handleInit = (api: IApi) => {
    apiRef.current = api;

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

        if (ev.file.type === "folder") {
          await createDirectory(instanceId, filePath);
          successToast("Folder created");
        } else {
          await createFile(instanceId, filePath, "");
          successToast("File created");
        }

        refreshCurrentPathRef.current();
        return false;
      } catch (error: any) {
        errorToast(ev.file.type === "folder" ? "Failed to create folder" : "Failed to create file", error);
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
        successToast("File uploaded");

        refreshCurrentPathRef.current();
        return false;
      } catch (error: any) {
        errorToast("Failed to upload file", error);
        return false;
      }
    });

    // Intercept delete to remove file(s)/folder(s) via our API
    // SVAR fires "delete-files" (plural) with {ids: TID[]}
    api.intercept("delete-files", async (ev: any) => {
      if (!ev?.ids?.length) return false;
      try {
        for (const id of ev.ids) {
          const rp = id === "/" ? ROOT_PATH : ROOT_PATH + id;
          await deleteFile(instanceId, rp);
          dirCacheRef.current.delete(id);
        }
        successToast(ev.ids.length > 1 ? `Deleted ${ev.ids.length} items` : "Deleted");
        refreshCurrentPathRef.current();
        return false;
      } catch (error: any) {
        errorToast("Failed to delete", error);
        return false;
      }
    });

    // Intercept download to trigger browser file download via our API
    api.intercept("download-file", async (ev: any) => {
      if (!ev?.id) return false;
      const rp = ev.id === "/" ? ROOT_PATH : ROOT_PATH + ev.id;
      const url = `/api/v1/instances/${instanceId}/files/download?path=${encodeURIComponent(rp)}`;
      const a = document.createElement("a");
      a.href = url;
      a.download = ev.id.split("/").pop() || "download";
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      return false;
    });

    // Intercept request-data (breadcrumbs refresh button) to reload current directory
    api.intercept("request-data", async (ev: any) => {
      if (!ev?.id) return false;
      const p = ev.id as string;
      const rp = p === "/" ? ROOT_PATH : ROOT_PATH + p;
      queryClient.invalidateQueries({
        queryKey: ["instances", instanceId, "files", "browse", rp],
      });
      return false;
    });

    // Intercept rename to move the file/folder via our API
    api.intercept("rename-file", async (ev: any) => {
      if (!ev?.id || !ev?.name) {
        return false;
      }

      try {
        const oldRealPath = ev.id === "/" ? ROOT_PATH : ROOT_PATH + ev.id;
        // Derive new path: same parent directory, new name
        const parentVirtual = ev.id.substring(0, ev.id.lastIndexOf("/")) || "/";
        const parentReal = parentVirtual === "/" ? ROOT_PATH : ROOT_PATH + parentVirtual;
        const newRealPath = parentReal + "/" + ev.name;

        await renameFile(instanceId, oldRealPath, newRealPath);
        successToast("Renamed");

        // Evict old path from cache
        dirCacheRef.current.delete(ev.id);
        refreshCurrentPathRef.current();
        return false;
      } catch (error: any) {
        errorToast("Failed to rename", error);
        return false;
      }
    });
  };

  const handleSaveFile = async () => {
    if (!selectedFile || editedContent === null) return;
    setIsSaving(true);
    try {
      const filePath = selectedFile === "/" ? ROOT_PATH : ROOT_PATH + selectedFile;
      await createFile(instanceId, filePath, editedContent);
      successToast("File saved");
      setEditedContent(null);
      // Invalidate the read cache so re-opening shows fresh content
      queryClient.invalidateQueries({
        queryKey: ["instances", instanceId, "files", "read"],
      });
    } catch (error: any) {
      errorToast("Failed to save file", error);
    } finally {
      setIsSaving(false);
    }
  };

  const handleCloseEditor = () => {
    setSelectedFile(null);
    setEditedContent(null);
  };

  if (fileData.length === 0) {
    return (
      <div className="flex items-center justify-center h-96 text-gray-500">
        Loading files...
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      <div className="flex flex-1 min-h-0">
        <div className="flex-1 min-w-0 overflow-hidden h-full">
          <Willow>
            <Filemanager data={fileData} mode={"table"} panels={panels} init={handleInit} />
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
    </div>
  );
}
