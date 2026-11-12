import assert from "node:assert/strict";
import test from "node:test";

import { createSub2ApiAttempt, SUB2API_ATTEMPT_TTL_MS } from "./sub2api-attempt.ts";

const credentials = { userID: 7, token: "synthetic-remote-token" };
const selection = {
  items: [{ keyID: 1, name: "Key", groupName: "Group", platform: "unknown" }],
  protocols: ["openai_responses" as const],
};

test("keeps credentials private to the active attempt and exposes pending protocol metadata", () => {
  let now = 1000;
  const attempt = createSub2ApiAttempt(3, credentials, () => now);
  assert.equal(attempt.expiresAt, 1000 + SUB2API_ATTEMPT_TTL_MS);
  assert.deepEqual(attempt.readCredentials(), credentials);
  assert.equal(attempt.readSelection(), null);
  assert.equal(attempt.requireProtocol(selection), true);
  assert.equal(attempt.readSelection(), selection);
  assert.equal(JSON.stringify(attempt).includes(credentials.token), false);
  now += 1;
  assert.deepEqual(attempt.readCredentials(), credentials);
});

test("success, cancellation, and supersession all drop credentials and pending selections", () => {
  for (const reason of ["succeeded", "cancelled", "superseded", "failed"] as const) {
    const attempt = createSub2ApiAttempt(3, credentials, () => 1000);
    attempt.requireProtocol(selection);
    attempt.close(reason);
    assert.equal(attempt.closeReason, reason);
    assert.equal(attempt.readCredentials(), null);
    assert.equal(attempt.readSelection(), null);
    assert.equal(attempt.requireProtocol(selection), false);
  }
});

test("expires credentials after five minutes", () => {
  let now = 5000;
  const attempt = createSub2ApiAttempt(4, credentials, () => now);
  now += SUB2API_ATTEMPT_TTL_MS - 1;
  assert.deepEqual(attempt.readCredentials(), credentials);
  now += 1;
  assert.equal(attempt.readCredentials(), null);
  assert.equal(attempt.closeReason, "expired");
  assert.equal(attempt.readSelection(), null);
  assert.equal(attempt.requireProtocol(selection), false);
});
