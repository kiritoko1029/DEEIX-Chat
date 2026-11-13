import type { Sub2ApiExternalProfile, UserDTO } from "../api/auth-types.ts";

export type UserProfileDisplayInput = Pick<UserDTO, "displayName" | "username" | "email" | "externalProfile">;

export type UserProfileDisplay = {
  name: string;
  email: string;
};

export type LocalUserPresentation = {
  source: "local";
  name: string;
  username: string;
  displayName: string;
  email: string;
  pendingSync: false;
};

export type Sub2ApiUserPresentation = {
  source: "sub2api";
  name: string;
  username: string;
  displayName: string;
  email: string;
  remoteID?: number;
  profileSyncedAt?: string | null;
  pendingSync: boolean;
};

export type UserPresentation = LocalUserPresentation | Sub2ApiUserPresentation;

function readSub2ApiProfile(user: UserProfileDisplayInput): Sub2ApiExternalProfile | null {
  return user.externalProfile?.provider === "sub2api" ? user.externalProfile : null;
}

export function resolveSub2ApiSourceEmail(user: UserProfileDisplayInput): string {
  return readSub2ApiProfile(user)?.email.trim() ?? "";
}

export function resolveUserPresentation(user: UserProfileDisplayInput): UserPresentation {
  const profile = readSub2ApiProfile(user);
  if (!profile) {
    const displayName = user.displayName.trim();
    const username = user.username.trim();
    return {
      source: "local",
      name: displayName,
      username,
      displayName,
      email: user.email.trim(),
      pendingSync: false,
    };
  }

  const username = profile.username.trim();
  const displayName = profile.displayName.trim();
  const email = profile.email.trim();
  return {
    source: "sub2api",
    name: username || displayName || email,
    username,
    displayName,
    email,
    remoteID: profile.remoteID,
    profileSyncedAt: profile.profileSyncedAt,
    pendingSync: profile.profileSyncedAt === null
      || (profile.profileSyncedAt === undefined && (!username || !email)),
  };
}

export function mergeUserResponseProfile<T extends { id: number; externalProfile?: Sub2ApiExternalProfile }>(previous: T, next: T): T {
  if (previous.id !== next.id || next.externalProfile !== undefined || previous.externalProfile?.provider !== "sub2api") {
    return next;
  }
  return { ...next, externalProfile: previous.externalProfile };
}

export function resolveUserProfileDisplay(user: UserProfileDisplayInput): UserProfileDisplay {
  const username = user.username.trim();
  const profile = readSub2ApiProfile(user);
  if (!profile) {
    return {
      name: user.displayName || username,
      email: user.email || username,
    };
  }

  const externalName = profile.username.trim() || profile.displayName.trim();
  const localName = user.displayName.trim();
  const safeInternalUsername = /^sub2api_[a-z0-9-]+$/i.test(username) ? "" : username;
  const name = externalName || localName || safeInternalUsername;
  return {
    name,
    email: profile.email.trim() || externalName || user.email.trim() || localName || safeInternalUsername,
  };
}
