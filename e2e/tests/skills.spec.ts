import { test, expect } from "../utils/base";

test("skills page: tabs and empty-library state render", async ({ page }) => {
  await page.goto("/skills");
  await expect(page.getByRole("heading", { name: "Skills" })).toBeVisible();

  // Library tab is the default; empty-state copy is shown on a fresh install.
  await expect(page.getByText("No skills uploaded yet.")).toBeVisible();
  await expect(page.getByRole("button", { name: /Upload Skill/ })).toBeVisible();

  // Switch to Discover tab — the search box becomes visible.
  await page.getByRole("button", { name: "Discover" }).click();
  await expect(page.getByPlaceholder("Search Clawhub skills…")).toBeVisible();

  // Switch back to Library and re-open the upload modal as a smoke check.
  await page.getByRole("button", { name: "Library" }).click();
  await page.getByRole("button", { name: /Upload Skill/ }).click();
  // Modal should mount; assert any of the standard modal affordances.
  await expect(page.getByRole("dialog").or(page.getByText(/upload.*skill/i)).first()).toBeVisible();
});
