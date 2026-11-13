import assert from "node:assert/strict";
import test from "node:test";

import * as profileSettings from "../utils/profile-settings.ts";

test("missing profile timezone defaults to Asia/Shanghai", () => {
  assert.equal(profileSettings.createDraftFromUser().timezone, "Asia/Shanghai");
  assert.equal(profileSettings.createDraftFromUser({ timezone: "" }).timezone, "Asia/Shanghai");
});

test("an explicit viewer timezone is preserved", () => {
  assert.equal(profileSettings.createDraftFromUser({ timezone: "Etc/UTC" }).timezone, "Etc/UTC");
  assert.equal(profileSettings.createDraftFromUser({ timezone: "America/New_York" }).timezone, "America/New_York");
});

test("sub2api settings saves exclude source identity and local display name", () => {
  assert.equal(typeof profileSettings.createProfileUpdatePayload, "function");
  const viewer = {
    username: "sub2api_internal_uuid",
    displayName: "Internal display",
    email: "security@example.test",
    externalProfile: {
      provider: "sub2api" as const,
      username: "source-user",
      displayName: "Source display",
      email: "source@example.test",
    },
  };
  const draft = {
    avatarUrl: "file:avatar-1",
    displayName: "Internal display",
    timezone: "Etc/UTC",
    locale: "zh-CN",
    profilePreferences: "Concise answers",
  };
  const payload = profileSettings.createProfileUpdatePayload(viewer, draft);
  assert.deepEqual(payload, {
    avatarURL: "file:avatar-1",
    timezone: "Etc/UTC",
    locale: "zh-CN",
    profilePreferences: "Concise answers",
  });
  assert.equal("username" in payload, false);
  assert.equal("email" in payload, false);
  assert.equal(JSON.stringify(payload).includes("source@example.test"), false);
  assert.equal(JSON.stringify(payload).includes("source-user"), false);
});

test("ordinary settings saves retain the editable local display name", () => {
  const draft = {
    avatarUrl: "",
    displayName: "New local display",
    timezone: "Asia/Shanghai",
    locale: "en-US",
    profilePreferences: "",
  };
  assert.deepEqual(
    profileSettings.createProfileUpdatePayload({ username: "local-user", displayName: "Old display", email: "" }, draft),
    {
      avatarURL: "",
      displayName: "New local display",
      timezone: "Asia/Shanghai",
      locale: "en-US",
      profilePreferences: "",
    },
  );
});
