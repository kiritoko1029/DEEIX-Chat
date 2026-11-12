import assert from "node:assert/strict";
import test from "node:test";

import { captureSub2ApiBootstrap, consumeSub2ApiBootstrap, hasSub2ApiBootstrap } from "./sub2api-bootstrap.ts";
import { parseSub2ApiLink, SUB2API_TOKEN_MAX_LENGTH } from "./sub2api-link.ts";

function location(pathname: string, search = "", hash = "") {
  return { pathname, search, hash };
}

test("ignores ordinary root visits without serializing unrelated parameters", () => {
  assert.deepEqual(parseSub2ApiLink(location("/", "?view=compact", "#messages")), { kind: "none" });
});

test("ignores sub2api-shaped parameters outside the root entry", () => {
  assert.deepEqual(parseSub2ApiLink(location("/chat", "?user_id=1&token=test-token")), { kind: "none" });
  assert.deepEqual(parseSub2ApiLink(location("/auth/callback", "?token=unrelated-oauth-value")), { kind: "none" });
});

test("captures query credentials and immediately derives a cleaned URL", () => {
  const result = parseSub2ApiLink(location("/", "?user_id=42&token=test-token&view=compact", "#messages"));
  assert.deepEqual(result, {
    kind: "exchange",
    credentials: { userID: 42, token: "test-token" },
    cleanedURL: "/?view=compact#messages",
  });
});

test("captures fragment credentials without touching unrelated fragment values", () => {
  const result = parseSub2ApiLink(location("/", "", "#user_id=7&token=test-token&view=compact"));
  assert.deepEqual(result, {
    kind: "exchange",
    credentials: { userID: 7, token: "test-token" },
    cleanedURL: "/#view=compact",
  });
});

test("accepts the largest safe integer and maximum token length", () => {
  const token = "x".repeat(SUB2API_TOKEN_MAX_LENGTH);
  const result = parseSub2ApiLink(location("/", `?user_id=9007199254740991&token=${token}`));
  assert.equal(result.kind, "exchange");
  if (result.kind === "exchange") {
    assert.equal(result.credentials.userID, Number.MAX_SAFE_INTEGER);
    assert.equal(result.credentials.token.length, SUB2API_TOKEN_MAX_LENGTH);
  }
  assert.equal(result.cleanedURL, "/");
});

test("rejects missing parameters and removes sensitive values", () => {
  assert.deepEqual(parseSub2ApiLink(location("/", "?user_id=1&view=compact")), {
    kind: "error",
    reason: "missing",
    cleanedURL: "/?view=compact",
  });
  assert.deepEqual(parseSub2ApiLink(location("/", "?token=test-token")), {
    kind: "error",
    reason: "missing",
    cleanedURL: "/",
  });
});

test("rejects duplicate parameters across query and fragment sources", () => {
  for (const value of [
    location("/", "?user_id=1&user_id=2&token=test-token"),
    location("/", "?user_id=1&token=one&token=two"),
    location("/", "?user_id=1&token=test-token", "#user_id=2"),
    location("/", "?user_id=1", "#user_id=1&token=test-token"),
  ]) {
    const result = parseSub2ApiLink(value);
    assert.equal(result.kind, "error");
    assert.equal(result.kind === "error" && result.reason, "duplicate");
    assert.equal(result.cleanedURL, "/");
  }
});

test("rejects non-positive, non-canonical, and unsafe user IDs", () => {
  for (const userID of ["", "0", "-1", "+1", "01", "1.5", "1e2", "9007199254740992", "abc"]) {
    const result = parseSub2ApiLink(location("/", `?user_id=${encodeURIComponent(userID)}&token=test-token`));
    assert.deepEqual(result, { kind: "error", reason: "invalid-user-id", cleanedURL: "/" });
  }
});

test("rejects empty and overlong tokens without retaining them in the result", () => {
  assert.deepEqual(parseSub2ApiLink(location("/", "?user_id=1&token=")), {
    kind: "error",
    reason: "invalid-token",
    cleanedURL: "/",
  });
  const token = "x".repeat(SUB2API_TOKEN_MAX_LENGTH + 1);
  const result = parseSub2ApiLink(location("/", `?user_id=1&token=${token}`));
  assert.deepEqual(result, { kind: "error", reason: "invalid-token", cleanedURL: "/" });
  assert.equal(JSON.stringify(result).includes(token), false);
});

test("browser capture replaces URL state before exposing in-memory credentials", () => {
  const replacements: Array<{ state: unknown; unused: string; url: string }> = [];
  const historyState = { existing: true };
  captureSub2ApiBootstrap(location("/", "?user_id=9&token=test-token"), {
    state: historyState,
    replaceState(state, unused, url) {
      replacements.push({ state, unused, url });
    },
  });

  assert.deepEqual(replacements, [{ state: null, unused: "", url: "/" }]);
  assert.equal(hasSub2ApiBootstrap(), true);
  assert.deepEqual(consumeSub2ApiBootstrap(), {
    kind: "exchange",
    credentials: { userID: 9, token: "test-token" },
    cleanedURL: "/",
  });
  assert.equal(hasSub2ApiBootstrap(), false);
  assert.equal(consumeSub2ApiBootstrap(), null);
});
