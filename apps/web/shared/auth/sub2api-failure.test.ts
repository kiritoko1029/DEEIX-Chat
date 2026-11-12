import assert from "node:assert/strict";
import test from "node:test";
import { classifySub2ApiFailure, sub2ApiFailureMessageKeys } from "./sub2api-failure.ts";

test("sub2api rejection details select only local diagnostic categories", () => {
  for (const [reason, expected] of [
    ["session_binding_mismatch", "session-binding"],
    ["token_expired", "token-rejected"],
    ["token_invalid", "token-rejected"],
    ["user_id_mismatch", "identity-mismatch"],
    ["key_owner_mismatch", "identity-mismatch"],
    ["user_inactive", "user-inactive"],
  ]) assert.equal(classifySub2ApiFailure("auth.sub2api_rejected", {reason}), expected);
  assert.equal(classifySub2ApiFailure("auth.sub2api_rejected", {stage:"keys",reason:"upstream_forbidden"}), "key-access-denied");
});

test("unknown or credential-bearing details never become display text", () => {
  for (const details of [null, "FAKE_SECRET", ["FAKE_SECRET"], {reason:"FAKE_SECRET",message:"FAKE_SECRET",stage:"FAKE_SECRET"}]) {
    const failure=classifySub2ApiFailure("auth.sub2api_rejected",details);
    assert.equal(failure,"token-rejected");
    assert.equal(sub2ApiFailureMessageKeys[failure],"tokenRejected");
  }
  assert.equal(classifySub2ApiFailure("FAKE_SECRET", {reason:"session_binding_mismatch"}), "exchange-failed");
});

test("configuration, import and rate failures are not mislabeled as expired tokens", () => {
  assert.equal(classifySub2ApiFailure("auth.sub2api_no_usable_key",null),"no-key");
  assert.equal(classifySub2ApiFailure("personal_provider.disabled",null),"service-disabled");
  assert.equal(classifySub2ApiFailure("personal_provider.blocked_host",null),"provider-unavailable");
  assert.equal(classifySub2ApiFailure("auth.sub2api_gateway_not_allowed",null),"gateway-not-allowed");
  assert.equal(classifySub2ApiFailure(undefined,null,429),"rate-limited");
});
