import assert from "node:assert/strict";
import test from "node:test";

import { resolveSub2ApiSourceEmail, resolveUserProfileDisplay } from "../../../shared/auth/external-profile.ts";
import { resolveEmailTitle, resolveEmailValue, shouldUseEmailBootstrap } from "./account-settings.ts";

test("ordinary account presentation and security email stay unchanged", () => {
  const user = {
    displayName: "Local User",
    username: "local",
    email: "{{TEST_EMAIL_1}}",
    emailSource: "local",
    emailVerifiedAt: null,
    emailBootstrapUsedAt: null,
  };
  assert.deepEqual(resolveUserProfileDisplay(user), { name: "Local User", email: "{{TEST_EMAIL_1}}" });
  assert.equal(resolveEmailTitle(user), "Email ({{TEST_EMAIL_1}})");
  assert.equal(resolveEmailValue(user, true), "Unverified");
});

test("sub2api username is the primary display name and empty external email falls back to it", () => {
  const user = {
    displayName: "sub2api 用户",
    username: "sub2api_0123456789abcdef",
    email: "",
    externalProfile: {
      provider: "sub2api" as const,
      displayName: "External display name",
      username: "external-nickname",
      email: "",
    },
  };
  assert.deepEqual(resolveUserProfileDisplay(user), { name: "external-nickname", email: "external-nickname" });
  assert.equal(resolveSub2ApiSourceEmail(user), "");
});

test("external email is display-only and never replaces the independent security email", () => {
  const user = {
    displayName: "sub2api 用户",
    username: "sub2api_0123456789abcdef",
    email: "",
    emailSource: "",
    emailVerifiedAt: null,
    emailBootstrapUsedAt: null,
    externalProfile: {
      provider: "sub2api" as const,
      displayName: "External display name",
      username: "external-nickname",
      email: "{{TEST_EMAIL_2}}",
    },
  };
  assert.deepEqual(resolveUserProfileDisplay(user), { name: "external-nickname", email: "{{TEST_EMAIL_2}}" });
  assert.equal(resolveSub2ApiSourceEmail(user), "{{TEST_EMAIL_2}}");
  assert.equal(resolveEmailTitle(user), "Email");
  assert.equal(resolveEmailValue(user, true), undefined);
  assert.equal(shouldUseEmailBootstrap(user), true);
});

test("external displayName and local non-generated identity remain safe fallbacks", () => {
  const external = {
    displayName: "Local fallback",
    username: "local-user",
    email: "",
    externalProfile: { provider: "sub2api" as const, displayName: "External fallback", username: "", email: "" },
  };
  assert.deepEqual(resolveUserProfileDisplay(external), { name: "External fallback", email: "External fallback" });
  const generated = {
    displayName: "",
    username: "sub2api_0123456789abcdef",
    email: "",
    externalProfile: { provider: "sub2api" as const, displayName: "", username: "", email: "" },
  };
  assert.deepEqual(resolveUserProfileDisplay(generated), { name: "", email: "" });
});
