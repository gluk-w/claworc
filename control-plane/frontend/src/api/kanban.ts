import client from "./client";

export interface KanbanBoard {
  id: number;
  name: string;
  description: string;
  eligible_instances: number[];
  created_at: string;
  updated_at: string;
}

export interface KanbanTask {
  id: number;
  board_id: number;
  title: string;
  description: string;
  status: "draft" | "todo" | "dispatching" | "in_progress" | "done" | "failed" | "archived";
  assigned_instance_id?: number | null;
  openclaw_session_id: string;
  evaluator_provider_key: string;
  evaluator_model: string;
  created_at: string;
  updated_at: string;
}

export interface KanbanComment {
  id: number;
  task_id: number;
  kind: string;
  author: string;
  body: string;
  openclaw_session_id: string;
  created_at: string;
  updated_at: string;
}

export interface KanbanArtifact {
  id: number;
  task_id: number;
  path: string;
  size_bytes: number;
  sha256: string;
  created_at: string;
}

export const kanbanApi = {
  listBoards: () => client.get<KanbanBoard[]>("/kanban/boards").then((r) => r.data),
  createBoard: (p: { name: string; description: string; eligible_instances: number[] }) =>
    client.post<KanbanBoard>("/kanban/boards", p).then((r) => r.data),
  getBoard: (id: number) =>
    client
      .get<KanbanBoard & { tasks: KanbanTask[] }>(`/kanban/boards/${id}`)
      .then((r) => r.data),
  updateBoard: (id: number, p: { name: string; description: string; eligible_instances: number[] }) =>
    client.put(`/kanban/boards/${id}`, p),
  deleteBoard: (id: number) => client.delete(`/kanban/boards/${id}`),
  createTask: (
    boardId: number,
    p: {
      title: string;
      description: string;
      evaluator_provider_key: string;
      evaluator_model: string;
      status?: "draft" | "todo";
    },
  ) => client.post<KanbanTask>(`/kanban/boards/${boardId}/tasks`, p).then((r) => r.data),
  startTask: (id: number) => client.post(`/kanban/tasks/${id}/start`),
  getTask: (id: number) =>
    client
      .get<{ task: KanbanTask; comments: KanbanComment[]; artifacts: KanbanArtifact[] }>(
        `/kanban/tasks/${id}`,
      )
      .then((r) => r.data),
  patchTask: (id: number, p: Partial<{ status: string; title: string; description: string }>) =>
    client.patch(`/kanban/tasks/${id}`, p),
  stopTask: (id: number) => client.post(`/kanban/tasks/${id}/stop`),
  deleteTask: (id: number) => client.delete(`/kanban/tasks/${id}`),
  reopenTask: (id: number) => client.post(`/kanban/tasks/${id}/reopen`),
  addUserComment: (id: number, body: string) =>
    client.post(`/kanban/tasks/${id}/comments`, { body }),
};
