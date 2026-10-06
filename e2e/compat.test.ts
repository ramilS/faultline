// Verifies @sentry/node — the query-string auth family (?sentry_key=…), the
// same transport code @sentry/browser uses — against a real, running
// Faultline. run.sh seeds the project/key, starts the server, and exports the
// env vars this file reads; it is not runnable standalone.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { test } from "node:test";
import * as Sentry from "@sentry/node";

function requireEnv(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`missing required env var ${name}`);
  }
  return value;
}

const dsn = requireEnv("FAULTLINE_E2E_DSN");
const projectId = requireEnv("FAULTLINE_E2E_PROJECT_ID");
const dbContainer = requireEnv("FAULTLINE_E2E_DB_CONTAINER");
const nonce = requireEnv("FAULTLINE_E2E_NONCE");

const outerMessage = `js outer failure ${nonce}`;
const innerMessage = `js inner cause ${nonce}`;
// Faultline's titleFor renders "Type: Value"; Error is the type Sentry's JS
// SDK assigns to a plain `new Error(...)`.
const expectedTitle = `Error: ${outerMessage}`;

Sentry.init({
  dsn,
  tracesSampleRate: 0,
  autoSessionTracking: false,
  integrations: [],
});

// The throw site (parseManifest) sits two calls below the entry point
// (startImport) on purpose, and wraps a cause. A passing assertion below is
// only possible if Faultline (a) reversed Sentry's oldest-first
// exception.values to title the issue after the OUTER exception, and (b)
// reversed Sentry's caller-to-callee frames to report the throw site — not
// the entry point — as culprit.
function readManifestFile(): never {
  throw new Error(innerMessage);
}

function parseManifest(): never {
  try {
    readManifestFile();
  } catch (cause) {
    throw new Error(outerMessage, { cause });
  }
}

function startImport(): void {
  parseManifest();
}

interface SeenIssue {
  title: string;
  culprit: string;
}

// Verification queries Postgres directly rather than the dashboard API: every
// dashboard route requires a session cookie, and standing up the login flow
// here would test that flow, not Sentry-protocol compatibility. Faultline
// stores title/culprit as plain issue columns, so this is the same
// established practice as seeding — SQL, no admin UI.
function queryIssue(): SeenIssue | undefined {
  const escapedTitle = expectedTitle.replace(/'/g, "''");
  const sql =
    `SELECT title || chr(31) || culprit FROM issues ` +
    `WHERE project_id = ${projectId} AND title = '${escapedTitle}' ` +
    `ORDER BY first_seen DESC LIMIT 1;`;
  const output = execFileSync(
    "docker",
    ["exec", "-i", dbContainer, "psql", "-tA", "-U", "faultline", "-d", "faultline", "-c", sql],
    { encoding: "utf8" },
  ).trim();
  if (!output) {
    return undefined;
  }
  const [title, culprit] = output.split("\x1f");
  return { title, culprit };
}

async function pollForIssue(timeoutMs: number): Promise<SeenIssue> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const found = queryIssue();
    if (found) {
      return found;
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(`no issue titled "${expectedTitle}" appeared within ${timeoutMs}ms`);
}

test("@sentry/node delivers a nested exception and Faultline reports the throw site as culprit", async (t) => {
  try {
    startImport();
  } catch (error) {
    Sentry.captureException(error);
  }

  const delivered = await Sentry.flush(5000);
  assert.equal(delivered, true, "Sentry.flush did not drain the transport queue");

  const issue = await pollForIssue(30_000);
  t.diagnostic(`observed issue: title=${JSON.stringify(issue.title)} culprit=${JSON.stringify(issue.culprit)}`);

  // The assumption the issue-title unit tests cannot verify on their own: Sentry
  // orders exception.values oldest-first, so the OUTER exception is the
  // last element and must be what the issue is titled after.
  assert.equal(issue.title, expectedTitle, "issue title must name the outer exception");

  // The core assertion: culprit must name the throwing function,
  // not the process entry point.
  assert.match(issue.culprit, /parseManifest/, "issue culprit must name the throw site, not the entry point");
});
