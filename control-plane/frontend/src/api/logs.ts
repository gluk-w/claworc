import client from "./client";

export async function fetchServerLogs(
    lines: number = 200,
): Promise<{ logs: string }> {
    const { data } = await client.get<{ logs: string }>("/logs", {
        params: { lines },
    });
    return data;
}

export async function clearServerLogs(): Promise<void> {
    await client.delete("/logs");
}
