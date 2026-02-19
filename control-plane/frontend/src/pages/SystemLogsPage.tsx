import { useState, useRef, useEffect } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, Trash2, Download } from "lucide-react";
import toast from "react-hot-toast";
import { fetchServerLogs, clearServerLogs } from "@/api/logs";

export default function SystemLogsPage() {
    const queryClient = useQueryClient();
    const [lines, setLines] = useState(200);
    const [autoScroll, setAutoScroll] = useState(true);
    const logRef = useRef<HTMLPreElement>(null);

    const {
        data,
        isLoading,
        refetch,
        isFetching,
    } = useQuery({
        queryKey: ["serverLogs", lines],
        queryFn: () => fetchServerLogs(lines),
        refetchInterval: 5_000,
    });

    const clearMut = useMutation({
        mutationFn: clearServerLogs,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ["serverLogs"] });
            toast.success("Logs cleared");
        },
        onError: () => toast.error("Failed to clear logs"),
    });

    // Auto-scroll to bottom when new data arrives
    useEffect(() => {
        if (autoScroll && logRef.current) {
            logRef.current.scrollTop = logRef.current.scrollHeight;
        }
    }, [data, autoScroll]);

    const handleDownload = () => {
        if (!data?.logs) return;
        const blob = new Blob([data.logs], { type: "text/plain" });
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `claworc-logs-${new Date().toISOString().slice(0, 19)}.txt`;
        a.click();
        URL.revokeObjectURL(url);
    };

    const logContent = data?.logs || "";
    const lineCount = logContent ? logContent.split("\n").length : 0;

    return (
        <div>
            <div className="flex items-center justify-between mb-4">
                <div>
                    <h1 className="text-xl font-semibold text-gray-900">System Logs</h1>
                    <p className="text-sm text-gray-500 mt-0.5">
                        Control plane server logs{lineCount > 0 && ` · ${lineCount} lines`}
                    </p>
                </div>
                <div className="flex items-center gap-2">
                    <select
                        value={lines}
                        onChange={(e) => setLines(Number(e.target.value))}
                        className="px-2 py-1.5 text-sm border border-gray-300 rounded-md bg-white"
                    >
                        <option value={100}>100 lines</option>
                        <option value={200}>200 lines</option>
                        <option value={500}>500 lines</option>
                        <option value={1000}>1000 lines</option>
                    </select>
                    <label className="flex items-center gap-1.5 text-sm text-gray-600 cursor-pointer select-none">
                        <input
                            type="checkbox"
                            checked={autoScroll}
                            onChange={(e) => setAutoScroll(e.target.checked)}
                            className="rounded border-gray-300"
                        />
                        Auto-scroll
                    </label>
                    <button
                        onClick={() => refetch()}
                        disabled={isFetching}
                        className="p-1.5 text-gray-500 hover:text-gray-700 rounded-md hover:bg-gray-100 disabled:opacity-40"
                        title="Refresh"
                    >
                        <RefreshCw size={16} className={isFetching ? "animate-spin" : ""} />
                    </button>
                    <button
                        onClick={handleDownload}
                        disabled={!logContent}
                        className="p-1.5 text-gray-500 hover:text-gray-700 rounded-md hover:bg-gray-100 disabled:opacity-40"
                        title="Download logs"
                    >
                        <Download size={16} />
                    </button>
                    <button
                        onClick={() => {
                            if (confirm("Clear all server logs?")) clearMut.mutate();
                        }}
                        disabled={clearMut.isPending}
                        className="flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-red-600 border border-red-200 rounded-md hover:bg-red-50 disabled:opacity-50"
                    >
                        <Trash2 size={14} />
                        Clear
                    </button>
                </div>
            </div>

            <div className="bg-gray-900 rounded-lg border border-gray-700 overflow-hidden">
                {isLoading ? (
                    <div className="p-6 text-gray-400 text-sm">Loading logs...</div>
                ) : !logContent ? (
                    <div className="p-6 text-gray-500 text-sm">No log entries</div>
                ) : (
                    <pre
                        ref={logRef}
                        className="p-4 text-xs leading-5 text-gray-200 font-mono overflow-auto max-h-[calc(100vh-180px)] whitespace-pre-wrap break-words"
                    >
                        {logContent}
                    </pre>
                )}
            </div>
        </div>
    );
}
