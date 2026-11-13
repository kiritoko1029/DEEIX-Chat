import assert from "node:assert/strict";
import test from "node:test";

import * as externalProfile from "./external-profile.ts";

test("ordinary users keep local display name, username, and email presentation", () => {
  assert.equal(typeof externalProfile.resolveUserPresentation, "function");
  assert.deepEqual(
    externalProfile.resolveUserPresentation({
      displayName: "Local display",
      username: "local-user",
      email: "local@example.test",
    }),
    {
      source: "local",
      name: "Local display",
      username: "local-user",
      displayName: "Local display",
      email: "local@example.test",
      pendingSync: false,
    },
  );
});

test("sub2api presentation prefers the source username and exposes the source email", () => {
  const display = externalProfile.resolveUserPresentation({
    displayName: "sub2api user",
    username: "sub2api_internal_uuid",
    email: "security@example.test",
    externalProfile: {
      provider: "sub2api" as const,
      username: "source-user",
      displayName: "Source display",
      email: "source@example.test",
      remoteID: 71,
      profileSyncedAt: "2026-11-13T01:02:03Z",
    },
  });
  assert.deepEqual(display, {
    source: "sub2api",
    name: "source-user",
    username: "source-user",
    displayName: "Source display",
    email: "source@example.test",
    remoteID: 71,
    profileSyncedAt: "2026-11-13T01:02:03Z",
    pendingSync: false,
  });
  assert.equal(JSON.stringify(display).includes("sub2api_internal_uuid"), false);
  assert.equal(JSON.stringify(display).includes("security@example.test"), false);
});

test("incomplete sub2api profiles use source fallbacks and are marked pending sync", () => {
  const input = {
    displayName: "sub2api user",
    username: "sub2api_internal_uuid",
    email: "",
    externalProfile: {
      provider: "sub2api" as const,
      username: "",
      displayName: "Source display",
      email: "",
      remoteID: 72,
      profileSyncedAt: null,
    },
  };
  assert.deepEqual(externalProfile.resolveUserPresentation(input), {
    source: "sub2api",
    name: "Source display",
    username: "",
    displayName: "Source display",
    email: "",
    remoteID: 72,
    profileSyncedAt: null,
    pendingSync: true,
  });
  assert.equal(
    externalProfile.resolveUserPresentation({
      ...input,
      externalProfile: { ...input.externalProfile, displayName: "", email: "source@example.test" },
    }).name,
    "source@example.test",
  );
  assert.equal(
    externalProfile.resolveUserPresentation({
      ...input,
      externalProfile: { ...input.externalProfile, displayName: "" },
    }).name,
    "",
  );
});

test("an omitted externalProfile in an update response preserves the previous source", () => {
  assert.equal(typeof externalProfile.mergeUserResponseProfile, "function");
  const profile = {
    provider: "sub2api" as const,
    username: "source-user",
    displayName: "Source display",
    email: "source@example.test",
  };
  const previous: {
    id: number;
    displayName: string;
    username: string;
    email: string;
    externalProfile?: typeof profile;
  } = {
    id: 1,
    displayName: "sub2api user",
    username: "sub2api_internal_uuid",
    email: "",
    externalProfile: profile,
  };
  const response = { ...previous, displayName: "Updated local display" };
  delete response.externalProfile;
  assert.deepEqual(externalProfile.mergeUserResponseProfile(previous, response), {
    ...response,
    externalProfile: previous.externalProfile,
  });
  const replacement = {
    ...response,
    externalProfile: { ...profile, username: "new-source-user" },
  };
  assert.equal(externalProfile.mergeUserResponseProfile(previous, replacement), replacement);
  const anotherUser = { ...response, id: 2 };
  assert.equal(externalProfile.mergeUserResponseProfile(previous, anotherUser), anotherUser);
});

test("a synchronized profile with an unset upstream username is not falsely pending", () => {
  const display = externalProfile.resolveUserPresentation({
    username: "sub2api_internal_uuid", displayName: "sub2api user", email: "",
    externalProfile: {
      provider: "sub2api", username: "", displayName: "", email: "source@example.test",
      remoteID: 7, profileSyncedAt: "2026-11-13T01:02:03Z",
    },
  });
  assert.equal(display.name, "source@example.test");
  assert.equal(display.pendingSync, false);
});
