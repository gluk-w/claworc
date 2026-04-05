import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import { createPortal } from "react-dom";
import { Filemanager, Willow, type IApi } from "@svar-ui/react-filemanager";
import "@svar-ui/react-filemanager/all.css";
import { useQueryClient } from "@tanstack/react-query";
import { FilePlus, FolderPlus, Upload, FolderUp } from "lucide-react";
import { successToast, errorToast } from "@/utils/toast";
import { useBrowseFiles, useReadFile } from "@/hooks/useFiles";
import { createFile, createDirectory, uploadFile, uploadDirectory, deleteFile, renameFile, copyFile } from "@/api/files";
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
  const [isUploading, setIsUploading] = useState(false);
  const apiRef = useRef<IApi | null>(null);
  const currentPathRef = useRef(initialPath);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const dirInputRef = useRef<HTMLInputElement>(null);
  const svarWrapperRef = useRef<HTMLDivElement>(null);
  const [portalTarget, setPortalTarget] = useState<HTMLDivElement | null>(null);
  const selectedFileRef = useRef(selectedFile);
  selectedFileRef.current = selectedFile;
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



    // Listen to set-path (runs after SVAR's internal handler) for folder navigation.
    // Only handle IDs that look like virtual paths (start with "/"). SVAR fires
    // set-path with internal IDs like "body" that aren't file paths.
    api.on("set-path", (ev: any) => {
      if (ev.id && ev.id.startsWith("/") && ev.id !== currentPathRef.current) {
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

    // Intercept file creation to prevent SVAR's default behavior.
    // SVAR also routes file uploads through "create-file" (there is no
    // separate "upload-file" event). When the event originates from an
    // upload, ev.file.file contains the native File blob.
    api.intercept("create-file", async (ev: any) => {
      if (!ev?.file?.name || !ev?.parent) {
        return false;
      }

      try {
        const parentPath = ev.parent === "/" ? ROOT_PATH : ROOT_PATH + ev.parent;

        if (ev.file.type === "folder") {
          const filePath = `${parentPath}/${ev.file.name}`;
          await createDirectory(instanceId, filePath);
          successToast("Folder created");
        } else if (ev.file.file instanceof File) {
          await uploadFile(instanceId, parentPath, ev.file.file);
          successToast("File uploaded");
        } else {
          const filePath = `${parentPath}/${ev.file.name}`;
          await createFile(instanceId, filePath, "");
          successToast("File created");
        }

        refreshCurrentPathRef.current();
        return false;
      } catch (error: any) {
        const action = ev.file.type === "folder" ? "create folder" : ev.file.file instanceof File ? "upload file" : "create file";
        errorToast(`Failed to ${action}`, error);
        return false;
      }
    });

    // Intercept delete to remove file(s)/folder(s) via our API.
    // We do NOT return false — SVAR's internal handler must run to clean up
    // its selection/panel state. We also do NOT call setFileData directly
    // because removing items while SVAR's internal state still references
    // them causes a crash in SVAR's init(). Instead we let the query refresh
    // rebuild fileData naturally through the useEffect.
    api.intercept("delete-files", async (ev: any) => {
      if (!ev?.ids?.length) return;
      try {
        for (const id of ev.ids) {
          const rp = id === "/" ? ROOT_PATH : ROOT_PATH + id;
          await deleteFile(instanceId, rp);
          dirCacheRef.current.delete(id);
        }
        // Clean deleted items from cached directory listings
        const idsSet = new Set<string>(ev.ids);
        for (const [dirPath, items] of dirCacheRef.current.entries()) {
          dirCacheRef.current.set(dirPath, items.filter(item => !idsSet.has(item.id)));
        }
        // Close editor if the selected file was deleted
        if (selectedFileRef.current && idsSet.has(selectedFileRef.current)) {
          setSelectedFile(null);
          setEditedContent(null);
        }
        successToast(ev.ids.length > 1 ? `Deleted ${ev.ids.length} items` : "Deleted");
        refreshCurrentPathRef.current();
      } catch (error: any) {
        errorToast("Failed to delete", error);
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

    // Intercept request-data (breadcrumbs refresh button) to reload current directory.
    // Only handle IDs that look like virtual paths (start with "/"). SVAR also fires
    // request-data with internal IDs like "body" that aren't file paths.
    api.intercept("request-data", async (ev: any) => {
      if (!ev?.id || !ev.id.startsWith("/")) return;
      const p = ev.id as string;
      const rp = p === "/" ? ROOT_PATH : ROOT_PATH + p;
      dirCacheRef.current.delete(p);
      queryClient.invalidateQueries({
        queryKey: ["instances", instanceId, "files", "browse", rp],
      });
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

    // Intercept copy-files to copy file(s)/folder(s) via our API
    api.intercept("copy-files", async (ev: any) => {
      if (!ev?.ids?.length || !ev?.target) return false;
      try {
        const targetReal = ev.target === "/" ? ROOT_PATH : ROOT_PATH + ev.target;
        for (const id of ev.ids) {
          const srcReal = id === "/" ? ROOT_PATH : ROOT_PATH + id;
          const name = id.split("/").pop();
          const dstReal = targetReal + "/" + name;
          await copyFile(instanceId, srcReal, dstReal);
        }
        successToast(ev.ids.length > 1 ? `Copied ${ev.ids.length} items` : "Copied");
        refreshCurrentPathRef.current();
        return false;
      } catch (error: any) {
        errorToast("Failed to copy", error);
        return false;
      }
    });

    // Intercept move-files (cut+paste) to move file(s)/folder(s) via our API
    api.intercept("move-files", async (ev: any) => {
      if (!ev?.ids?.length || !ev?.target) return false;
      try {
        const targetReal = ev.target === "/" ? ROOT_PATH : ROOT_PATH + ev.target;
        for (const id of ev.ids) {
          const srcReal = id === "/" ? ROOT_PATH : ROOT_PATH + id;
          const name = id.split("/").pop();
          const dstReal = targetReal + "/" + name;
          await renameFile(instanceId, srcReal, dstReal);
          dirCacheRef.current.delete(id);
        }
        successToast(ev.ids.length > 1 ? `Moved ${ev.ids.length} items` : "Moved");
        refreshCurrentPathRef.current();
        return false;
      } catch (error: any) {
        errorToast("Failed to move", error);
        return false;
      }
    });
  };

  // Inject a portal container into SVAR's sidebar, replacing the "Add New" button
  useEffect(() => {
    const wrapper = svarWrapperRef.current;
    if (!wrapper) return;
    const sidebarWrapper = wrapper.querySelector<HTMLElement>(".wx-FlucfALM.wx-wrapper");
    if (!sidebarWrapper) return;
    // Hide the original "Add New" button
    const addNewBtn = sidebarWrapper.querySelector<HTMLElement>(":scope > .wx-button");
    if (addNewBtn) addNewBtn.style.display = "none";
    // Insert our portal container at the top
    const container = document.createElement("div");
    sidebarWrapper.insertBefore(container, sidebarWrapper.firstChild);
    setPortalTarget(container);
    return () => {
      container.remove();
      if (addNewBtn) addNewBtn.style.display = "";
    };
  }, [fileData]);

  const handleNewFile = async () => {
    const name = window.prompt("File name:");
    if (!name) return;
    try {
      await createFile(instanceId, `${realPath}/${name}`, "");
      successToast("File created");
      refreshCurrentPathRef.current();
    } catch (error: any) {
      errorToast("Failed to create file", error);
    }
  };

  const handleNewFolder = async () => {
    const name = window.prompt("Folder name:");
    if (!name) return;
    try {
      await createDirectory(instanceId, `${realPath}/${name}`);
      successToast("Folder created");
      refreshCurrentPathRef.current();
    } catch (error: any) {
      errorToast("Failed to create folder", error);
    }
  };

  const handleUploadFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      await uploadFile(instanceId, realPath, file);
      successToast("File uploaded");
      refreshCurrentPathRef.current();
    } catch (error: any) {
      errorToast("Failed to upload file", error);
    }
    e.target.value = "";
  };

  const handleUploadDirectory = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files;
    if (!files || files.length === 0) return;
    setIsUploading(true);
    try {
      const result = await uploadDirectory(instanceId, realPath, files);
      successToast(`Uploaded ${result.count} files`);
      refreshCurrentPathRef.current();
    } catch (error: any) {
      errorToast("Failed to upload directory", error);
    } finally {
      setIsUploading(false);
      e.target.value = "";
    }
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

  const sidebarToolbar = portalTarget && createPortal(
    <div className="flex items-center gap-1 px-2 py-1.5">
      <button
        onClick={handleNewFile}
        className="p-1 text-gray-400 hover:text-gray-600 transition-colors"
        title="New file"
      >
        <FilePlus size={16} />
      </button>
      <button
        onClick={handleNewFolder}
        className="p-1 text-gray-400 hover:text-gray-600 transition-colors"
        title="New folder"
      >
        <FolderPlus size={16} />
      </button>
      <button
        onClick={() => fileInputRef.current?.click()}
        className="p-1 text-gray-400 hover:text-gray-600 transition-colors"
        title="Upload file"
      >
        <Upload size={16} />
      </button>
      <button
        onClick={() => dirInputRef.current?.click()}
        disabled={isUploading}
        className="p-1 text-gray-400 hover:text-gray-600 transition-colors disabled:opacity-50"
        title="Upload directory"
      >
        <FolderUp size={16} />
      </button>
      {isUploading && <span className="text-xs text-gray-400 ml-1">Uploading...</span>}
    </div>,
    portalTarget,
  );

  return (
    <div className="h-full flex flex-col">
      {sidebarToolbar}
      <input ref={fileInputRef} type="file" className="hidden" onChange={handleUploadFile} />
      <input ref={dirInputRef} type="file" className="hidden" onChange={handleUploadDirectory} {...{ webkitdirectory: "", directory: "" } as any} multiple />
      <div className="flex flex-1 min-h-0">
        <div ref={svarWrapperRef} className="flex-1 min-w-0 overflow-hidden h-full">
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
