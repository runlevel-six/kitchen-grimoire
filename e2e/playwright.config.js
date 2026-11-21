// SPDX-License-Identifier: Apache-2.0
// Browser tests at phone and tablet sizes (docs/DESIGN.md §16, P4.7).
import { defineConfig, devices } from "@playwright/test";
import os from "node:os";
import path from "node:path";
import fs from "node:fs";

const APP = 18555;
const SITE = 18556;
const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "grimoire-e2e-"));

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  fullyParallel: false, // one server, one database
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${APP}`,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "iphone-webkit", use: { ...devices["iPhone 13"] } },
    { name: "ipad-chromium", use: { ...devices["Galaxy Tab S4"], viewport: { width: 1024, height: 1366 }, isMobile: false } },
  ],
  webServer: [
    {
      // Builds and runs the real server with auth off (dev mode) and a fresh database.
      command: `cd .. && go build -o bin/grimoire-e2e ./cmd/grimoire && ./bin/grimoire-e2e serve`,
      url: `http://127.0.0.1:${APP}/readyz`,
      reuseExistingServer: false,
      timeout: 120_000,
      env: {
        GRIMOIRE_DEV: "1",
        GRIMOIRE_AUTH_MODE: "none",
        GRIMOIRE_LISTEN: `127.0.0.1:${APP}`,
        GRIMOIRE_ADMIN_LISTEN: `127.0.0.1:${APP + 100}`,
        GRIMOIRE_BASE_URL: `http://127.0.0.1:${APP}`,
        GRIMOIRE_DATA_DIR: dataDir,
        GRIMOIRE_FETCH_ALLOW_HOSTS: "127.0.0.1", // test-only: lets import reach the fixture site
      },
    },
    {
      // A stand-in recipe website with invented content.
      command: `node fixtures/site.mjs ${SITE}`,
      url: `http://127.0.0.1:${SITE}/health`,
      reuseExistingServer: false,
    },
  ],
});

export const SITE_URL = `http://127.0.0.1:${SITE}`;
