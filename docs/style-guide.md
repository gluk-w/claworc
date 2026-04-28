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
5. [Sticky Action Bar](#5-sticky-action-bar)
6. [Modals](#6-modals)
7. [Banners and Notices](#7-banners-and-notices)
8. [Typography](#8-typography)
9. [Loading and Disabled States](#9-loading-and-disabled-states)
10. [Toasts](#10-toasts)
11. [Page Header with Action Button](#11-page-header-with-action-button)
12. [Tab Navigation](#12-tab-navigation)
13. [Tables](#13-tables)
14. [Card Grid](#14-card-grid)
15. [Status Badges / Pills](#15-status-badges--pills)
16. [Search Input](#16-search-input)
17. [Icon-only Row Actions](#17-icon-only-row-actions)
18. [Large Scrollable Modal](#18-large-scrollable-modal-separated-headerfooter)
19. [Empty State (multi-line)](#19-empty-state-multi-line)
20. [Checkbox / Radio Styling](#20-checkbox--radio-styling)

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
- Settings and form-heavy pages constrain content to `max-w-2xl` using `space-y-8` between sections.
- Data-heavy pages with tables (e.g., Instances, Backups) use full width — omit the `max-w-*` class.
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

### Keyboard shortcuts for edit-in-place inputs

All edit-in-place inputs must handle **Enter** (save) and **Escape** (cancel). Use the
`EditInput` component (`src/components/EditInput.tsx`) instead of a raw `<input>` — it is a
drop-in replacement that adds `onSave` and `onCancel` props:

```tsx
import EditInput from "@/components/EditInput";

<EditInput
  type="text"
  value={pendingValue ?? ""}
  onChange={(e) => setPendingValue(e.target.value)}
  onSave={handleSave}
  onCancel={() => { setEditing(false); setPendingValue(null); }}
  className="..."
/>
```

Key rules:
- Always use `<EditInput>` — never wire up `onKeyDown` for Enter/Escape manually.
- `onSave` is called on **Enter**. The handler itself guards against invalid input, so no extra check is needed.
- `onCancel` is called on **Escape**. It should exit edit mode and clear pending state — same logic as the Cancel button.
- Apply to every input in an edit-in-place row, including multi-field groups (e.g., Resources grid — each input gets the shared save/cancel callbacks).
- All standard `<input>` props (including a custom `onKeyDown`) are forwarded.

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

## 5. Sticky Action Bar

Long forms (Settings, Create Instance, anything with multiple cards stacked under `space-y-8`)
must surface their primary action through `StickyActionBar`
(`src/components/StickyActionBar.tsx`) instead of an inline button row at the bottom of the page.

### Behavior

- Fixed to the bottom of the viewport, spanning from the right edge of the sidebar (`left-16`)
  to the right edge of the screen.
- **Hidden by default** — only slides up when the page reports it has something to commit (i.e.,
  dirty AND the action would be enabled). A pristine page shows nothing; there is no permanent footer.
- Slides in/out via a 200 ms transform transition. When hidden, `pointer-events` is disabled so
  invisible buttons can't be clicked.
- Sits at `z-30` — below modals (`z-50`) and toasts, above page content.
- The inner button row is constrained to `max-w-2xl mx-auto` so its buttons line up with the
  form they belong to.

### When to use

- Pages with multiple stacked sections / cards where the user would otherwise have to scroll
  to reach Save / Create.
- Any new "Create / Edit Entity" form.
- Settings-style pages with a single global Save action.

### When NOT to use

- **Modals** — they already have a fixed footer pattern (see Section 6); use that.
- **Inline-edit fields** (e.g., `EditInput` rows in `InstanceDetailPage`) — they save on Enter
  and don't need a bar.
- **Sections that save independently** of the page-level action (e.g., `EnvVarsEditor` in
  `SettingsPage` keeps its own in-card Save because env-var saves trigger instance restarts).
- **Pages that immediately commit on toggle** (e.g., a single checkbox like Anonymous Analytics).

### API

```tsx
type Props = {
  visible: boolean;            // controls slide-in/out
  children: React.ReactNode;   // typically Cancel + primary buttons
};
```

The component does NOT manage button styling, dirty state, or labels — the parent owns all of
that. Buttons use the same classes as elsewhere (see Section 4): `px-4 py-2 text-sm font-medium`,
blue primary, white-bordered secondary. The component already wraps children in
`flex justify-end gap-3`, so the parent supplies just the buttons.

### Page-level (single Save button)

```tsx
const hasChanges = /* compare current state to initial values */;

<StickyActionBar visible={hasChanges}>
  <button
    onClick={handleSave}
    disabled={updateMutation.isPending || !hasChanges}
    className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
  >
    {updateMutation.isPending ? "Saving..." : "Save Settings"}
  </button>
</StickyActionBar>
```

No Cancel button at the page level unless the page has a distinct "discard changes" concept;
sidebar navigation is the implicit way out.

### Form-level (Cancel + Submit pair)

Place the bar inside the `<form>` so `type="submit"` continues to fire `handleSubmit`.
`position: fixed` removes the bar from layout flow but does not break event bubbling.

```tsx
<form onSubmit={handleSubmit} className="space-y-8 pb-24">
  {/* ...sections... */}

  <StickyActionBar visible={isValid /* required-field check */}>
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
  </StickyActionBar>
</form>
```

Cancel on the left, primary action on the right.

### Required parent adjustments

- Add `pb-24` to the form / page's outer scroll container so the last section isn't clipped
  behind the bar when it's visible.
- For form-level usage, keep a back-link or breadcrumb at the top of the page so users can
  leave a pristine form (the bar is hidden then, so there is no Cancel button reachable from it).

### Reference implementations

- `SettingsPage.tsx` — page-level pattern.
- `InstanceForm.tsx` (used by `CreateInstancePage`) — form-level pattern.

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

---

## 11. Page Header with Action Button

Used in `UsersPage.tsx` (Create User), `SkillsPage.tsx` (Upload Skill).

```tsx
<div className="flex items-center justify-between mb-6">
  <h1 className="text-xl font-semibold text-gray-900">Page Title</h1>
  <button className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700">
    Action
  </button>
</div>
```

- Action button uses `px-3 py-1.5 text-sm` (smaller than page-level save, no border).
- If the button should be conditionally hidden but preserve layout, use `invisible` not conditional rendering.

---

## 12. Tab Navigation

Used in `SkillsPage.tsx` (Library/Discover), `InstanceDetailPage.tsx` (Overview/Browser/Terminal/…).

```tsx
<div className="flex border-b border-gray-200 mb-6">
  <button className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
    active ? "border-blue-600 text-blue-600" : "border-transparent text-gray-500 hover:text-gray-700"
  }`}>
    Tab Label
  </button>
</div>
```

- Container: `flex border-b border-gray-200 mb-6`
- Active: `border-blue-600 text-blue-600`
- Inactive: `border-transparent text-gray-500 hover:text-gray-700`

---

## 13. Tables

Used in `UsersPage.tsx`, `ProviderTable.tsx`, `InstanceTable.tsx`.

```tsx
<div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
  <table className="w-full text-sm">
    <thead className="bg-gray-50 border-b border-gray-200">
      <tr>
        <th className="text-left px-4 py-3 font-medium text-gray-600">Header</th>
      </tr>
    </thead>
    <tbody>
      <tr className="border-b border-gray-100 last:border-0">
        <td className="px-4 py-3 text-gray-900">{value}</td>
      </tr>
    </tbody>
  </table>
</div>
```

- Wrapper: `bg-white rounded-lg border border-gray-200 overflow-hidden`
- `<thead>`: `bg-gray-50 border-b border-gray-200`
- `<th>`: `text-left px-4 py-3 font-medium text-gray-600`
- `<tr>`: `border-b border-gray-100 last:border-0`
- `<td>`: `px-4 py-3`; primary column uses `text-gray-900`, secondary uses `text-gray-500`

---

## 14. Card Grid

Used in `SkillsPage.tsx` (Library and Discover tabs).

```tsx
<div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
  <div className="bg-white border border-gray-200 rounded-xl p-4 flex flex-col gap-2 hover:shadow-sm hover:border-blue-300 hover:bg-blue-50 transition-all cursor-pointer">
    {/* card content */}
  </div>
</div>
```

- Grid: `grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4`
- Card: `bg-white border border-gray-200 rounded-xl p-4 flex flex-col gap-2 hover:shadow-sm hover:border-blue-300 hover:bg-blue-50 transition-all cursor-pointer`
- Use `rounded-xl` (not `rounded-lg`) for cards.

---

## 15. Status Badges / Pills

Used in `StatusBadge.tsx`, `UsersPage.tsx`, `SkillsPage.tsx`.

**Colored status badge** (running/stopped/error):
```tsx
<span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-800">
  running
</span>
```
Color map: `running → bg-green-100 text-green-800`, `stopped → bg-gray-100 text-gray-600`, `error → bg-red-100 text-red-800`, `pending → bg-yellow-100 text-yellow-800`.

**Role / label badge**:
```tsx
<span className="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium rounded-full bg-purple-50 text-purple-700">
  admin
</span>
```

**Monospace version badge**:
```tsx
<span className="text-xs bg-gray-100 text-gray-500 px-2 py-0.5 rounded-full">v1.2</span>
```

---

## 16. Search Input

Used in `SkillsPage.tsx`, `ModelCatalogPicker.tsx`.

```tsx
<div className="relative mb-6">
  <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
  <input
    type="text"
    placeholder="Search…"
    className="w-full pl-9 pr-4 py-2.5 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
  />
  {isFetching && (
    <Loader2 size={14} className="absolute right-3 top-1/2 -translate-y-1/2 animate-spin text-gray-400" />
  )}
</div>
```

- Left icon: `pl-9` input padding, icon at `left-3`.
- Right async spinner: overlay at `right-3`.
- Use `py-2.5` for standalone search bars (slightly taller than regular inputs `py-1.5`).
- Use `rounded-lg` and `focus:border-transparent` for search inputs.

---

## 17. Icon-only Row Actions

Used in `UsersPage.tsx`, `SkillCard.tsx`, `InstanceTable.tsx`.

```tsx
<button
  onClick={handler}
  className="p-1 text-gray-400 hover:text-gray-600 transition-colors"
  title="Tooltip"
>
  <Icon size={14} />
</button>
```

- For destructive actions: `hover:text-red-600`.
- Always include a `title` attribute for accessibility.
- Include `e.stopPropagation()` when the button is inside a clickable parent (e.g., a card row).

---

## 18. Large Scrollable Modal (separated header/footer)

Used in `DeployModal.tsx`. Variant of the standard modal when the body may overflow.

```tsx
<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
  <div className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4 flex flex-col max-h-[80vh]">
    {/* Header */}
    <div className="px-6 py-4 border-b border-gray-200">
      <h2 className="text-base font-semibold text-gray-900">Title</h2>
      {description && <p className="text-sm text-gray-500 mt-1">{description}</p>}
    </div>
    {/* Scrollable body */}
    <div className="overflow-y-auto flex-1 px-6 py-4 flex flex-col gap-2">
      {/* content */}
    </div>
    {/* Footer */}
    <div className="px-6 py-4 border-t border-gray-200 flex items-center justify-end gap-3">
      <button className="px-4 py-2 text-sm font-medium text-gray-700 hover:text-gray-900 transition-colors">Cancel</button>
      <button className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors">Confirm</button>
    </div>
  </div>
</div>
```

- `rounded-xl shadow-xl` (vs `rounded-lg` for standard modals).
- `max-h-[80vh]` + `flex flex-col` + `overflow-y-auto flex-1` on body enables scroll.
- Header/footer get `border-b`/`border-t` separators with `px-6 py-4`.
- Footer buttons use `text-sm px-4 py-2` (same as page-level buttons, not `text-xs`).

---

## 19. Empty State (multi-line)

Used in `SkillsPage.tsx`, `DashboardPage.tsx`.

```tsx
<div className="text-center py-16 text-gray-400">
  <p className="text-sm">Primary empty message.</p>
  <p className="text-xs mt-1">Secondary hint or call-to-action.</p>
</div>
```

- `py-16` for generous vertical centering.
- Primary line: `text-sm`; secondary line: `text-xs mt-1`.

---

## 20. Checkbox / Radio Styling

Used in `DeployModal.tsx`, `ProviderTable.tsx`.

```tsx
<input
  type="checkbox"
  className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
/>
```

Same classes apply to `type="radio"`.

---

## 21. Multi-Select (`MultiSelect` component)

Reusable component at `src/components/MultiSelect.tsx`. Wraps `react-select` with all styles
pre-configured to match the design system. Used in `SharedFoldersPage.tsx`.

```tsx
import MultiSelect from "@/components/MultiSelect";

const options = items.map((i) => ({ value: i.id, label: i.name }));
const selected = options.filter((o) => selectedIds.includes(o.value));

<div>
  <label className="block text-xs text-gray-500 mb-1">Label</label>
  <MultiSelect
    options={options}
    value={selected}
    onChange={(sel) => setSelectedIds(sel.map((s) => s.value))}
    placeholder="Select items..."
    noOptionsMessage={() => "No items available"}
  />
</div>
```

The component handles `isMulti`, `styles`, and `menuPortalTarget` internally — do not pass them.
All other `react-select` props (`isClearable`, `isDisabled`, `isLoading`, etc.) are forwarded.

### Visual spec

| Element | Idle | Focused / Hover |
|---|---|---|
| Control border | `#d1d5db` (gray-300) | `#3b82f6` (blue-500) + ring shadow |
| Placeholder | `#9ca3af` (gray-400) 0.875rem | — |
| Multi-value pill bg | `#eff6ff` (blue-50) | — |
| Multi-value pill text | `#1d4ed8` (blue-700) 0.75rem | — |
| Pill remove icon | `#93c5fd` (blue-300) | `#2563eb` (blue-600) on `#dbeafe` (blue-100) |
| Option | `white` | `#eff6ff` (blue-50); active `#dbeafe` (blue-100) |
| Dropdown / clear icons | `#9ca3af` (gray-400) | `#6b7280` (gray-500) |
| Indicator separator | hidden | — |
| Menu | `border #e5e7eb`, `rounded-md`, `shadow-lg`, `z-index: 9999` | — |

Key rules:
- Always use `<MultiSelect>` — never use raw `react-select` imports.
- The menu portals to `document.body` with `z-index: 9999` so it renders above modals.
- Label uses `text-xs text-gray-500 mb-1` (same as other form labels).
- Options use `{ value: number, label: string }` shape (`MultiSelectOption` type is exported).
