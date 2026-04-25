package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
	"github.com/go-chi/chi/v5"
)

// TaskMgr is the global task manager, wired from main.go.
var TaskMgr *taskmanager.Manager

// rbacFilter returns a predicate that decides whether a task is visible to
// the caller. Admins see everything; non-admins see tasks whose InstanceID
// is in the caller's assigned instance set. Tasks with InstanceID == 0
// are admin-only.
func rbacFilter(r *http.Request) (func(taskmanager.Task) bool, error) {
	user := middleware.GetUser(r)
	if user == nil {
		return nil, errors.New("no user")
	}
	if user.Role == "admin" {
		return func(taskmanager.Task) bool { return true }, nil
	}
	ids, err := database.GetUserInstances(user.ID)
	if err != nil {
		return nil, err
	}
	allow := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		allow[id] = struct{}{}
	}
	return func(t taskmanager.Task) bool {
		if t.InstanceID == 0 {
			return false
		}
		_, ok := allow[t.InstanceID]
		return ok
	}, nil
}

// ListTasks returns tasks the caller is allowed to see, filtered by query
// params: type, instance_id, resource_id, state, only_active.
func ListTasks(w http.ResponseWriter, r *http.Request) {
	if TaskMgr == nil {
		writeJSON(w, http.StatusOK, []taskmanager.Task{})
		return
	}
	allow, err := rbacFilter(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := r.URL.Query()
	f := taskmanager.Filter{
		Type:       taskmanager.TaskType(q.Get("type")),
		ResourceID: q.Get("resource_id"),
		State:      taskmanager.State(q.Get("state")),
		OnlyActive: q.Get("only_active") == "true",
	}
	if v := q.Get("instance_id"); v != "" {
		var id uint
		fmt.Sscanf(v, "%d", &id)
		f.InstanceID = id
	}
	tasks := TaskMgr.List(f)
	out := make([]taskmanager.Task, 0, len(tasks))
	for _, t := range tasks {
		if allow(t) {
			out = append(out, t)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// GetTask returns a single task by ID.
func GetTask(w http.ResponseWriter, r *http.Request) {
	if TaskMgr == nil {
		writeError(w, http.StatusNotFound, "Task not found")
		return
	}
	id := chi.URLParam(r, "id")
	t, ok := TaskMgr.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "Task not found")
		return
	}
	allow, err := rbacFilter(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !allow(t) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// CancelTask cancels a running task if permitted.
func CancelTask(w http.ResponseWriter, r *http.Request) {
	if TaskMgr == nil {
		writeError(w, http.StatusNotFound, "Task not found")
		return
	}
	id := chi.URLParam(r, "id")
	t, ok := TaskMgr.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "Task not found")
		return
	}
	allow, err := rbacFilter(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !allow(t) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	err = TaskMgr.Cancel(id)
	switch {
	case errors.Is(err, taskmanager.ErrNotCancellable):
		writeError(w, http.StatusMethodNotAllowed, "Task is not cancellable")
		return
	case errors.Is(err, taskmanager.ErrAlreadyTerminal):
		writeError(w, http.StatusConflict, "Task has already finished")
		return
	case errors.Is(err, taskmanager.ErrNotFound):
		writeError(w, http.StatusNotFound, "Task not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "cancel requested"})
}

// StreamTaskEvents is an SSE endpoint that emits task lifecycle events the
// caller is allowed to see.
func StreamTaskEvents(w http.ResponseWriter, r *http.Request) {
	if TaskMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "Task manager not initialized")
		return
	}
	allow, err := rbacFilter(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Subscribe BEFORE sending the seed snapshot so we don't miss events
	// that fire between the snapshot and the subscription.
	ch, unsub := TaskMgr.Subscribe()
	defer unsub()

	flusher.Flush()

	// Seed with currently active tasks.
	for _, t := range TaskMgr.List(taskmanager.Filter{OnlyActive: true}) {
		if !allow(t) {
			continue
		}
		writeTaskEvent(w, flusher, taskmanager.Event{Type: taskmanager.EventStarted, Task: t})
	}

	ctx := r.Context()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !allow(ev.Task) {
				continue
			}
			writeTaskEvent(w, flusher, ev)
		case <-ctx.Done():
			return
		}
	}
}

func writeTaskEvent(w http.ResponseWriter, f http.Flusher, ev taskmanager.Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}
