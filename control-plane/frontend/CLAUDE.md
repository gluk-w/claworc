The dev server `http://localhost:5173` proxies `/api` and `/health` to `http://127.0.0.1:8000` (the Go backend).

## Toasts

All toasts use `AppToast` (`src/components/AppToast.tsx`) as the single toast component. It accepts `title`, optional `description`, `status` (`"success" | "error" | "info" | "loading"`), and `toastId`.

For one-shot toasts, use the helpers in `src/utils/toast.ts`:
- `successToast(title, description?)` — green check, 3s
- `errorToast(title, error?)` — red X, 5s; extracts detail from Axios errors automatically
- `infoToast(title, description?)` — blue info, 3s

For persistent/updating toasts (e.g. creation progress), call `toast.custom(createElement(AppToast, {...}), { id, duration })` directly with a stable `id` so subsequent calls update the same toast. See `useCreationToast` in `src/hooks/useInstances.ts` for the pattern.
