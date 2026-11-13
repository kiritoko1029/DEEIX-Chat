import assert from "node:assert/strict";
import test from "node:test";

import {
  classifySub2ApiPasswordFailure,
  isSub2ApiPasswordFlowExpiryValid,
  parseSub2ApiPasswordLoginOptions,
  parseSub2ApiPasswordProgress,
  parseSub2ApiPasswordProtocolRequired,
  parseSub2ApiRemoteTwoFactorRetry,
  SUB2API_PASSWORD_FLOW_TTL_MS,
} from "./sub2api-password-login.ts";

const flowID = "a".repeat(43);
const now = Date.parse("2026-11-13T01:00:00.000Z");
const expiresAt = "2026-11-13T01:04:00.000Z";
const options = {
  enabled: true,
  passwordLoginAvailable: true,
  browserOnly: true,
  unavailableReason: null,
  upstreamVersion: "0.2.21",
  remoteTotpEnabled: true,
  captcha: { provider: "turnstile", siteKey: "public-site-key" },
  fallbackURL: "https://sub.example.com/login",
};
const remote = { stage: "remote_2fa", flowID, expiresAt, maskedEmail: "u***@example.com" };
const user = { id: 2 };

test("parses only browser none/Turnstile options and safe trusted fallback URLs", () => {
  assert.deepEqual(parseSub2ApiPasswordLoginOptions(options), { ...options, fallbackURL: "https://sub.example.com/login" });
  assert.equal(parseSub2ApiPasswordLoginOptions({ ...options, captcha: { provider: "tencent", siteKey: "x" } }), null);
  assert.equal(parseSub2ApiPasswordLoginOptions({ ...options, captcha: { provider: "turnstile", siteKey: "" } }), null);
  assert.equal(parseSub2ApiPasswordLoginOptions({ ...options, browserOnly: false }), null);
  assert.equal(parseSub2ApiPasswordLoginOptions({ ...options, unavailableReason: { raw: "secret" } }), null);
  assert.equal(parseSub2ApiPasswordLoginOptions({ ...options, fallbackURL: "javascript:alert(1)" }), null);
  assert.equal(parseSub2ApiPasswordLoginOptions({ ...options, fallbackURL: "https://user:pass@example.com" }), null);
  const disabled = parseSub2ApiPasswordLoginOptions({ ...options, enabled: false, passwordLoginAvailable: false, fallbackURL: "", captcha: { provider: "none", siteKey: "" } });
  assert.equal(disabled?.enabled, false);
});

test("keeps remote 2FA, local 2FA and authenticated as strict distinct stages", () => {
  assert.deepEqual(parseSub2ApiPasswordProgress(remote), { ...remote, expiresAtMs: Date.parse(expiresAt) });
  assert.equal(parseSub2ApiPasswordProgress({ ...remote, accessToken: "must-not-be-accepted" }), null);
  assert.equal(parseSub2ApiPasswordProgress({ ...remote, flowID: "short" }), null);
  assert.equal(parseSub2ApiPasswordProgress({ ...remote, stage: "authenticated", login: { accessToken: "remote-token" } }), null);
  assert.equal(parseSub2ApiPasswordProgress({ stage: "local_2fa", login: { user, twoFactorRequired: false } }), null);
  const local = parseSub2ApiPasswordProgress({ stage: "local_2fa", login: { user, twoFactorRequired: true, twoFactorChallengeToken: "local-challenge" } });
  assert.equal(local?.stage, "local_2fa");
  const authenticated = parseSub2ApiPasswordProgress({ stage: "authenticated", login: { user, twoFactorRequired: false, accessToken: "chat-access", sessionID: "chat-session" } });
  assert.equal(authenticated?.stage, "authenticated");
});

test("requires flow metadata and the existing bounded Key selection on protocol 409", () => {
  const details = {
    stage: "protocol_selection",
    flowID,
    expiresAt,
    protocolSelection: {
      items: [{ keyID: 1, name: "DeepSeek", groupName: "Custom", platform: "unknown" }],
      protocols: ["openai_responses", "anthropic_messages", "openai_chat_completions"],
    },
  };
  const parsed = parseSub2ApiPasswordProtocolRequired({ status: 409, errorCode: "auth.sub2api_protocol_required", details });
  assert.equal(parsed?.flowID, flowID);
  assert.equal(parsed?.selection.items[0]?.name, "DeepSeek");
  assert.equal(parseSub2ApiPasswordProtocolRequired({ status: 409, errorCode: "auth.sub2api_protocol_required", details: { ...details, stage: "remote_2fa" } }), null);
  assert.equal(parseSub2ApiPasswordProtocolRequired({ status: 409, errorCode: "auth.sub2api_protocol_required", details: { ...details, flowID: "bad" } }), null);
  assert.equal(parseSub2ApiPasswordProtocolRequired({ status: 409, errorCode: "auth.sub2api_protocol_required", details: { ...details, protocolSelection: { ...details.protocolSelection, items: [details.protocolSelection.items[0], details.protocolSelection.items[0]] } } }), null);
});

test("remote code retry requires the original structured challenge and never extends its TTL", () => {
  assert.deepEqual(parseSub2ApiRemoteTwoFactorRetry({ status: 400, errorCode: "auth.sub2api_remote_two_factor_invalid", details: remote }), { ...remote, expiresAtMs: Date.parse(expiresAt) });
  assert.equal(parseSub2ApiRemoteTwoFactorRetry({ status: 400, errorCode: "auth.sub2api_remote_two_factor_invalid", details: { ...remote, flowID: "b".repeat(43) } })?.flowID, "b".repeat(43));
  assert.equal(parseSub2ApiRemoteTwoFactorRetry({ status: 401, errorCode: "auth.sub2api_remote_two_factor_invalid", details: remote }), null);
  assert.equal(isSub2ApiPasswordFlowExpiryValid(Date.parse(expiresAt), now), true);
  assert.equal(isSub2ApiPasswordFlowExpiryValid(now, now), false);
  assert.equal(isSub2ApiPasswordFlowExpiryValid(now + SUB2API_PASSWORD_FLOW_TTL_MS, now), true);
  assert.equal(isSub2ApiPasswordFlowExpiryValid(now + SUB2API_PASSWORD_FLOW_TTL_MS + 1, now), false);
});

test("maps only fixed local error categories without retaining upstream text", () => {
  assert.deepEqual(classifySub2ApiPasswordFailure({ status: 401, errorCode: "auth.sub2api_invalid_credentials", details: { raw: "secret" } }), { legacy: false, reason: "invalid-credentials" });
  assert.deepEqual(classifySub2ApiPasswordFailure({ status: 400, errorCode: "auth.sub2api_captcha_failed" }), { legacy: false, reason: "captcha-failed" });
  assert.deepEqual(classifySub2ApiPasswordFailure({ status: 410, errorCode: "auth.sub2api_login_flow_expired" }), { legacy: false, reason: "flow-expired" });
  assert.deepEqual(classifySub2ApiPasswordFailure({ status: 429, errorCode: "anything", details: "secret" }), { legacy: false, reason: "rate-limited" });
  assert.deepEqual(classifySub2ApiPasswordFailure({ status: 401, errorCode: "auth.sub2api_rejected", details: { reason: "user_id_mismatch" } }), { legacy: true, reason: "identity-mismatch" });
  assert.deepEqual(classifySub2ApiPasswordFailure(new Error("network")), { legacy: false, reason: "unavailable" });
});
