// SPDX-License-Identifier: Apache-2.0
import { test, expect } from "@playwright/test";

const SITE = "http://127.0.0.1:18556";

// A unique title per run and project, since tests share one database.
const title = (t) => `Example ${t} ${test.info().project.name}`;
const slugOf = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");

test("create, view, scale, search, and favorite a recipe", async ({ page }) => {
  const name = title("Lemon Bars");
  await page.goto("/new");
  await page.fill("#f-title", name);
  await page.fill("#f-yieldText", "24 bars");
  await page.fill("#f-servings", "24");
  await page.fill("#ig-lines-0", "2 cups flour\n1 8-oz pkg cream cheese, softened\npinch of salt");
  await page.fill("#sg-text-0", "Mix.\n\nBake 18-20 minutes.");
  await page.click("button[value=save]");
  await expect(page).toHaveURL(/created=1/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(name);

  // Scale 2× (htmx swap; works without JS too).
  await page.getByRole("button", { name: "2×" }).click();
  await expect(page.locator("#ingredients")).toContainText("4 cups flour");
  await expect(page.locator("#ingredients")).toContainText("2 8-oz pkg cream cheese");
  await expect(page.locator("#ingredients")).toContainText("not scaled");

  // Check-off survives a reload (sessionStorage).
  const first = page.locator(".ingredients input[type=checkbox]").first();
  await first.check();
  await page.reload();
  await expect(page.locator(".ingredients input[type=checkbox]").first()).toBeChecked();
  await page.locator(".js-clear-checks").first().click();
  await expect(page.locator(".ingredients input[type=checkbox]").first()).not.toBeChecked();

  // Favorite.
  await page.getByRole("button", { name: /Favorite/ }).click();
  await expect(page.getByRole("button", { name: "★ Favorite" })).toBeVisible();

  // Search finds it by an ingredient.
  await page.goto("/search?q=" + encodeURIComponent("cream cheese"));
  await expect(page.getByRole("link", { name: new RegExp(name) })).toBeVisible();
});

test("cook mode: steps, swipe-free navigation, and a timer", async ({ page }) => {
  const name = title("Chili");
  await page.goto("/new");
  await page.fill("#f-title", name);
  await page.fill("#ig-lines-0", "1 lb beef");
  await page.fill("#sg-text-0", "Brown the beef.\n\nSimmer 25 minutes.\n\nServe.");
  await page.click("button[value=save]");
  await page.getByRole("link", { name: "Start cooking" }).click();
  await expect(page.locator("#cook-count")).toHaveText("Step 1 of 3");
  await page.getByRole("link", { name: "Next →" }).click();
  await expect(page.locator("#cook-count")).toHaveText("Step 2 of 3");
  await page.getByRole("button", { name: /Start a timer for 25 minutes/ }).click();
  await expect(page.locator(".timer")).toHaveCount(1);
  await page.getByRole("link", { name: "← Back" }).click();
  await expect(page.locator("#cook-count")).toHaveText("Step 1 of 3");
});

test("import a recipe from a link", async ({ page }) => {
  await page.goto("/import");
  await page.fill("#f-url", SITE + "/cornbread");
  await page.getByRole("button", { name: "Get recipe" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Check and save");
  await expect(page.locator("#f-title")).toHaveValue("Example Skillet Cornbread");
  // Make the title unique per project so the second run doesn't collide.
  await page.fill("#f-title", title("Skillet Cornbread"));
  await page.click("button[value=save]");
  await expect(page).toHaveURL(/created=1/);
  await expect(page.locator(".provenance")).toContainText("Example Test Kitchen");
  await expect(page.locator("img.hero")).toBeVisible();
});

test("trash and undo", async ({ page }) => {
  const name = title("Temporary");
  await page.goto("/new");
  await page.fill("#f-title", name);
  await page.click("button[value=save]");
  await page.getByRole("button", { name: "Move to trash" }).click();
  await expect(page.getByText("Moved to the trash.")).toBeVisible();
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(page).toHaveURL(new RegExp("/r/" + slugOf(name)));
  await expect(page.getByText("Restored.")).toBeVisible();
});

test("PWA: manifest, service worker, and offline reading", async ({ page, context, browserName }) => {
  test.skip(browserName !== "chromium", "service worker offline checks run in Chromium");
  const res = await page.request.get("/static/manifest.webmanifest");
  expect(res.headers()["content-type"]).toContain("application/manifest+json");

  const name = title("Offline Toast");
  await page.goto("/new");
  await page.fill("#f-title", name);
  await page.fill("#ig-lines-0", "2 slices bread");
  await page.click("button[value=save]");
  const url = page.url().split("?")[0];
  // Wait for the worker to control the page, then load once more so it caches.
  await page.waitForFunction(() => navigator.serviceWorker && navigator.serviceWorker.controller);
  await page.goto(url);
  await context.setOffline(true);
  await page.goto(url);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(name);
  await expect(page.locator("#offline-banner")).toBeVisible();
  await context.setOffline(false);
});

test("editor autosaves a draft and offers to restore it", async ({ page }) => {
  const name = title("Draft Muffins");
  await page.goto("/new");
  await page.fill("#f-title", name);
  await page.fill("#ig-lines-0", "2 cups flour");
  await page.waitForTimeout(900); // autosave is debounced
  await page.reload();
  await expect(page.locator("#draft-banner")).toBeVisible();
  await page.getByRole("button", { name: "Restore them" }).click();
  await expect(page.locator("#f-title")).toHaveValue(name);
  await expect(page.locator("#ig-lines-0")).toHaveValue("2 cups flour");
  await page.click("button[value=save]");
  await expect(page).toHaveURL(/created=1/);
  // Saved: the draft is gone.
  await page.goto("/new");
  await expect(page.locator("#draft-banner")).toBeHidden();
});
