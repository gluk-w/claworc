import client from "./client";

export type TaskType =
  | "instance.create"
  | "instance.restart"
  | "instance.image_update"
  | "instance.clone"
  | "backup.create"
  | "skill.deploy"
  | "browser.spawn"
  | "browser.migrate";

export type TaskState = "running" | "succeeded" | "failed" | "canceled";

export interface Task {
  id: string;
  type: TaskType;
  instance_id?: number;
  resource_id?: string;
  resource_name?: string;
  title: string;
  state: TaskState;
  message?: string;
  cancellable?: boolean;
  started_at: string;
  ended_at?: string;
}

export type TaskEventType = "started" | "updated" | "ended";

export interface TaskEvent {
  type: TaskEventType;
  task: Task;
}

export interface ListTasksParams {
  type?: TaskType;
  instance_id?: number;
  resource_id?: string;
  state?: TaskState;
  only_active?: boolean;
}

export async function listTasks(params: ListTasksParams = {}): Promise<Task[]> {
  const res = await client.get<Task[]>("/tasks", { params });
  return res.data ?? [];
}

export async function getTask(id: string): Promise<Task> {
  const res = await client.get<Task>(`/tasks/${id}`);
  return res.data;
}

export async function cancelTask(id: string): Promise<void> {
  await client.post(`/tasks/${id}/cancel`);
}
