import type { Sub2ApiExternalProfile, UserDTO } from "../api/auth-types.ts";

export type UserProfileDisplayInput = Pick<UserDTO, "displayName" | "username" | "email" | "externalProfile">;

export type UserProfileDisplay = {
  name: string;
  email: string;
};

function readSub2ApiProfile(user: UserProfileDisplayInput): Sub2ApiExternalProfile | null {
  return user.externalProfile?.provider === "sub2api" ? user.externalProfile : null;
}

export function resolveSub2ApiSourceEmail(user: UserProfileDisplayInput): string {
  return readSub2ApiProfile(user)?.email.trim() ?? "";
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
