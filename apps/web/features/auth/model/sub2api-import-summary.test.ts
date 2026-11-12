import assert from "node:assert/strict";
import test from "node:test";

import type { Sub2ApiImportSummary } from "../../../shared/api/auth-types.ts";
import { createSub2ApiImportSummaryView, parseSub2ApiImportSummary, readSub2ApiImportSummaryFromDetails } from "./sub2api-import-summary.ts";

import { clearPendingSub2ApiSummary, rememberPendingSub2ApiSummary, takePendingSub2ApiSummary } from "./sub2api-pending-summary.ts";

const rawSecret = "api_key={{SECRET_hmyzm2gg}} raw upstream message";
const summary: Sub2ApiImportSummary = {
  total: 8,
  eligible: 8,
  imported: 1,
  reused: 1,
  skipped: 4,
  failed: 2,
  items: [
    { keyID: 1, name: "Key A", groupName: "OpenAI", providerID: "provider001", status: "imported" },
    { keyID: 2, name: "Key B", groupName: "Anthropic", providerID: "provider002", status: "reused" },
    { keyID: 3, name: "Key C", groupName: "OpenAI", status: "skipped", reason: "quota_exhausted" },
    { keyID: 4, name: "Key D", groupName: "Gemini", status: "failed", reason: "probe_failed" },
    { keyID: 5, name: "Key E", groupName: "Other", status: "skipped", reason: "unsupported_group" },
    { keyID: 6, name: "Key F", groupName: "OpenAI", status: "skipped", reason: "two_factor_required" },
    { keyID: 7, name: "Key G", groupName: "OpenAI", status: "failed", reason: rawSecret },
    { keyID: 8, name: "Key H", groupName: "DeepSeek", status: "skipped", reason: "user_skipped" },
  ],
};

test("summary exposes counts and fixed reason categories without raw errors or credentials", () => {
  const view = createSub2ApiImportSummaryView(summary);
  assert.equal(view.readyCount, 2);
  assert.equal(view.hasIssues, true);
  assert.deepEqual(view.issues.map((issue) => issue.reason), ["quota", "probe", "unsupported", "two-factor", "other", "user-skipped"]);
  assert.equal(view.issues[0]?.label, "Key C · OpenAI · #3");
  assert.equal(JSON.stringify(view).includes(rawSecret), false);
  assert.equal(JSON.stringify(view).includes("{{SECRET_hmyzm2gg}}"), false);
});

test("provider limit and import budget are distinct from upstream Key quota", () => {
  const view = createSub2ApiImportSummaryView({
    total: 3, eligible: 3, imported: 0, reused: 0, skipped: 0, failed: 3,
    items: ["limit_reached", "timeout", "quota_exhausted"].map((reason, index) => ({ keyID: index + 1, name: "Key", groupName: "Group", status: "failed", reason })),
  });
  assert.deepEqual(view.issues.map(item => item.reason), ["limit", "timeout", "quota"]);
});

test("two-factor summaries are memory-only and consumed only for the matching challenge", () => {
  clearPendingSub2ApiSummary();
  rememberPendingSub2ApiSummary("synthetic-challenge-a", summary);
  assert.equal(takePendingSub2ApiSummary("synthetic-challenge-b"), null);
  rememberPendingSub2ApiSummary("synthetic-challenge-a", summary);
  assert.deepEqual(takePendingSub2ApiSummary("synthetic-challenge-a"), summary);
  assert.equal(takePendingSub2ApiSummary("synthetic-challenge-a"), null);
  rememberPendingSub2ApiSummary("synthetic-challenge-a", summary);
  clearPendingSub2ApiSummary();
  assert.equal(takePendingSub2ApiSummary("synthetic-challenge-a"), null);
});

test("fully successful summaries support the concise ready message", () => {
  const view = createSub2ApiImportSummaryView({
    total: 2,
    eligible: 2,
    imported: 1,
    reused: 1,
    skipped: 0,
    failed: 0,
    items: summary.items.slice(0, 2),
  });
  assert.equal(view.readyCount, 2);
  assert.equal(view.hasIssues, false);
  assert.deepEqual(view.issues, []);
});

test("zero-success error details accept the same summary shape without reading unrelated errors", () => {
  const details = { sub2apiImport: summary, rawMessage: rawSecret };
  assert.deepEqual(readSub2ApiImportSummaryFromDetails(details), summary);
  assert.equal(readSub2ApiImportSummaryFromDetails({ error: rawSecret }), null);
  assert.equal(parseSub2ApiImportSummary(null), null);
  assert.equal(parseSub2ApiImportSummary({ total: 1 }), null);
});
