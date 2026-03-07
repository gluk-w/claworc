# Claworc UI Style Guide

This guide defines the UI patterns for the Claworc control-plane frontend. All AI agents
modifying or creating UI must follow these conventions exactly. The Settings page
(`SettingsPage.tsx`) is the canonical reference implementation.

---

## Table of Contents

1. [Page Layout](#1-page-layout)
2. [Sections (Cards)](#2-sections-cards)
3. [Form Elements](#3-form-elements)
4. [Buttons](#4-buttons)
5. [Save / Cancel Bar](#5-save--cancel-bar)
6. [Modals](#6-modals)
7. [Banners and Notices](#7-banners-and-notices)
8. [Typography](#8-typography)
9. [Loading and Disabled States](#9-loading-and-disabled-states)
10. [Toasts](#10-toasts)

---

## 1. Page Layout

```
<div>
  <h1 className="text-xl font-semibold text-gray-900 mb-6">Page Title</h1>

  {/* optional banner */}

  <div className="space-y-8 max-w-2xl">
    {/* sections go here */}
  </div>
</div>
```

- The outermost wrapper is a plain `<div>` with no extra padding (the sidebar layout provides it).
- The page title uses `text-xl font-semibold text-gray-900 mb-6`.
- All content is constrained to `max-w-2xl` using `space-y-8` between sections.
- Do **not** add a container background, shadow, or extra padding to the outer wrapper.

---

## 2. Sections (Cards)

Each logical group of settings lives in its own card.

```tsx
<div className="bg-white rounded-lg border border-gray-200 p-6">
  <h3 className="text-sm font-medium text-gray-900 mb-4">Section Title</h3>
  {/* content */}
</div>
```

### Section header with an action button (e.g., "Add Provider")

```tsx
<div className="flex items-center justify-between mb-4">
  <h3 className="text-sm font-medium text-gray-900">Section Title</h3>
  <button
    type="button"
    className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
  >
    <PlusIcon size={12} />
    Add Something
  </button>
</div>
```

### Section header with an icon

```tsx
<h3 className="text-sm font-medium text-gray-900 flex items-center gap-1.5 mb-2">
  <KeyIcon size={14} />
  Section Title
</h3>
```

Use `mb-4` when the next element is a field; use `mb-2` when a description paragraph follows the title.

### Section description

```tsx
<p className="text-xs text-gray-500 mb-3">Short description of what this section does.</p>
```

### Grouped fields inside a section

- Single-column fields: `<div className="space-y-4">` wrapping individual field rows.
- Two-column grids: `<div className="grid grid-cols-2 gap-4">`.

---

## 3. Form Elements

### Text / password input

```tsx
<div>
  <label className="block text-xs text-gray-500 mb-1">Label Text</label>
  <input
    type="text"
    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
    placeholder="Hint text"
  />
</div>
```

Key rules:
- `text-xs text-gray-500` for labels; always `block` so it wraps on its own line.
- `mb-1` between label and input.
- All inputs are full-width: `w-full`.
- Padding: `px-3 py-1.5` (compact, not the taller `py-2`).
- Ring on focus: `focus:outline-none focus:ring-2 focus:ring-blue-500`.
- Required-field labels append ` *` in the label text (no HTML `required` asterisk styling).

### Password input with show/hide toggle

```tsx
<div className="relative">
  <input
    type={show ? "text" : "password"}
    className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
  />
  <button
    type="button"
    onClick={() => setShow(!show)}
    className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
  >
    {show ? <EyeOffIcon size={14} /> : <EyeIcon size={14} />}
  </button>
</div>
```

Add `pr-10` to the input so text does not overlap the toggle icon.

### Select (dropdown)

```tsx
<select
  className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
>
  <option value="" disabled hidden></option>
  {/* options */}
</select>
```

Use an empty, `disabled hidden` first option as placeholder for create-mode selects.

### Helper / hint text (below an input)

```tsx
<p className="text-xs text-gray-400 mt-1">
  Key: <span className="font-mono">{derivedKey}</span>
</p>
```

### Read-only monospace display

When showing a computed or masked value (fingerprints, API key previews):

```tsx
<div className="bg-gray-50 border border-gray-200 rounded-md p-3">
  <dt className="text-xs text-gray-500 mb-0.5">Label</dt>
  <dd className="text-xs font-mono text-gray-900 break-all">{value}</dd>
</div>
```

### Inline "Change" link (edit-in-place pattern)

```tsx
<div className="flex items-center gap-2">
  <span className="text-sm text-gray-500 font-mono">{maskedValue}</span>
  <button type="button" onClick={() => setEditing(true)} className="text-xs text-blue-600 hover:text-blue-800">
    Change
  </button>
</div>
```

When editing, replace the row with the input + Cancel button (see the Brave API Key section in
`SettingsPage.tsx` for the full pattern).

---

## 4. Buttons

There are four button variants used across the UI. Sizes are consistent by context.

### Primary (blue — confirm / save / create)

```tsx
className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
```

Used for the main affirmative action on a page or modal.

### Secondary (ghost / outline — cancel, secondary actions)

```tsx
className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
```

Used for Cancel in forms and for non-destructive secondary actions.

### Danger (red — destructive confirm)

```tsx
className="px-4 py-2 text-sm font-medium text-white bg-red-600 rounded-md hover:bg-red-700"
```

Used in confirmation dialogs for irreversible actions (delete, overwrite).

### Danger outline (red border — soft destructive, e.g., Delete inside a modal)

```tsx
className="px-3 py-1.5 text-xs font-medium text-red-600 border border-red-200 rounded-md hover:bg-red-50 disabled:opacity-50"
```

### Small secondary (for section header actions like "Add Provider", "Rotate Key")

```tsx
className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
```

### Size summary

| Context | Padding | Font size |
|---|---|---|
| Page-level save/cancel | `px-4 py-2` | `text-sm` |
| Modal confirm/cancel | `px-3 py-1.5` | `text-xs` |
| Section header action | `px-3 py-1.5` | `text-xs` |
| Inline cancel (edit-in-place) | `px-3 py-1.5` | `text-xs` |

### Button loading state

Replace the label with a present-participle string. Never remove the button.

```tsx
<button disabled={mutation.isPending} className="...">
  {mutation.isPending ? "Saving..." : "Save Settings"}
</button>
```

Loading text follows the pattern `<Verb>ing...`: `Saving...`, `Creating...`, `Deleting...`,
`Rotating...`.

### Icon buttons

Use Lucide icons. Small icon size inside buttons: `size={12}` for `text-xs` buttons, `size={14}`
for `text-sm` buttons. Icons are positioned with `inline-flex items-center gap-1.5`.

---

## 5. Save / Cancel Bar

### Page-level (single Save button)

When a settings page has a single global save action, place a right-aligned save button **below**
all sections, outside any card:

```tsx
<div className="flex justify-end">
  <button
    onClick={handleSave}
    disabled={updateMutation.isPending || !hasChanges}
    className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
  >
    {updateMutation.isPending ? "Saving..." : "Save Settings"}
  </button>
</div>
```

- `justify-end` — Save sits on the right.
- No Cancel button at the page level unless the page has a distinct "discard changes" concept.
- Disable when there are no pending changes (`!hasChanges`).

### Form-level (Cancel + Submit pair)

For forms that create or edit an entity (new instance, new item):

```tsx
<div className="flex justify-end gap-3">
  <button
    type="button"
    onClick={onCancel}
    className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
  >
    Cancel
  </button>
  <button
    type="submit"
    disabled={loading || !isValid}
    className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
  >
    {loading ? "Creating..." : "Create"}
  </button>
</div>
```

- **Cancel is on the left, primary action is on the right.**
- Both buttons are `text-sm`, `px-4 py-2`.
- Wrap in `flex justify-end gap-3`.

---

## 6. Modals

```tsx
{open && (
  <div className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center">
    <div className="bg-white rounded-lg shadow-xl p-6 w-full max-w-md mx-4">
      <h2 className="text-base font-semibold text-gray-900 mb-4">Modal Title</h2>

      <div className="space-y-4">
        {/* form fields */}
      </div>

      {/* Modal footer */}
      <div className="flex items-center justify-between mt-6">
        <div className="flex gap-2">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          {/* optional destructive action on the left group */}
          <button
            type="button"
            className="px-3 py-1.5 text-xs font-medium text-red-600 border border-red-200 rounded-md hover:bg-red-50"
          >
            Delete
          </button>
        </div>
        <button
          type="button"
          onClick={onSave}
          disabled={!canSave}
          className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
        >
          Save
        </button>
      </div>
    </div>
  </div>
)}
```

### Modal footer layout

```
[ Cancel ]  [ Delete ]          [ Save ]
 ← left group                  right →
```

- The footer uses `flex items-center justify-between`.
- **Cancel and Delete (if present) are grouped on the left** inside a `flex gap-2` div.
- **The primary action (Save/Confirm) is on the right**, alone.
- All modal buttons use `px-3 py-1.5 text-xs`.
- Modal backdrop: `bg-black/40`, **not** `bg-black/50` (slightly lighter than the confirm dialog).

### Confirmation dialog (destructive, no form)

```tsx
<div className="fixed inset-0 z-50 flex items-center justify-center">
  <div className="fixed inset-0 bg-black/50" onClick={onCancel} />
  <div className="relative bg-white rounded-lg shadow-lg p-6 max-w-sm w-full mx-4">
    <h3 className="text-lg font-semibold text-gray-900 mb-2">{title}</h3>
    <p className="text-sm text-gray-600 mb-6">{message}</p>
    <div className="flex justify-end gap-3">
      <button
        onClick={onCancel}
        className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
      >
        Cancel
      </button>
      <button
        onClick={onConfirm}
        className="px-4 py-2 text-sm font-medium text-white bg-red-600 rounded-md hover:bg-red-700"
      >
        Confirm
      </button>
    </div>
  </div>
</div>
```

- Backdrop: `bg-black/50`; clicking it calls `onCancel`.
- Footer: `flex justify-end gap-3` — **Cancel left, Confirm (red) right**.
- Buttons are `text-sm px-4 py-2` (larger than modal form buttons).
- Max width `max-w-sm` (narrower than a form modal's `max-w-md`).

---

## 7. Banners and Notices

### Warning banner (amber)

```tsx
<div className="flex items-center gap-2 px-3 py-2 mb-6 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
  <AlertTriangle size={16} className="shrink-0" />
  Warning message text.
</div>
```

Place immediately after the page `<h1>`, before the `max-w-2xl` content wrapper.

### Error inline (red text)

```tsx
<p className="text-xs text-red-600">Error description.</p>
```

### Loading inline

```tsx
<p className="text-xs text-gray-400">Loading...</p>
```

### Empty state

```tsx
<p className="text-sm text-gray-400 italic">No items configured.</p>
```

---

## 8. Typography

| Role | Classes |
|---|---|
| Page heading | `text-xl font-semibold text-gray-900` |
| Section/card heading | `text-sm font-medium text-gray-900` |
| Modal heading | `text-base font-semibold text-gray-900` |
| Confirm dialog heading | `text-lg font-semibold text-gray-900` |
| Label | `text-xs text-gray-500` |
| Helper / secondary text | `text-xs text-gray-400` |
| Body / description | `text-sm text-gray-600` |
| Monospace value | `font-mono` (combined with the appropriate size/color) |
| Code badge / pill | `text-xs font-mono text-gray-400 bg-gray-100 px-1.5 py-0.5 rounded` |
| Link / action text | `text-xs text-blue-600 hover:text-blue-800` |

---

## 9. Loading and Disabled States

- Disabled inputs/buttons always add `disabled:opacity-50 disabled:cursor-not-allowed`.
- Spinning icons use `className={isPending ? "animate-spin" : ""}` on the icon element.
- Skeleton/loading pages: `<div className="text-center py-12 text-gray-500">Loading...</div>`.

---

## 10. Toasts

Use the helpers from `src/utils/toast.ts`. Never use raw `react-hot-toast` calls for one-shot notifications.

```ts
successToast("Operation succeeded");           // green, 3 s
errorToast("Operation failed", axiosError);    // red, 5 s, auto-extracts detail
infoToast("FYI message");                      // blue, 3 s
```

For persistent/updating toasts (e.g., progress during multi-step creation):

```ts
toast.custom(createElement(AppToast, { title, status: "loading", toastId: id }), { id, duration: Infinity });
// later:
toast.custom(createElement(AppToast, { title: "Done", status: "success", toastId: id }), { id, duration: 3000 });
```

See `useCreationToast` in `src/hooks/useInstances.ts` for the canonical pattern.
