import type { ProfileDraft } from "@/features/settings/types/settings";
import type { PatchMePayload, UserDTO } from "@/shared/api/auth-types";
import { normalizeTrimmedString } from "../../../shared/lib/string.ts";

export function createDraftFromUser(user?: Partial<UserDTO> | null): ProfileDraft {
  return {
    avatarUrl: normalizeTrimmedString(user?.avatarURL),
    displayName: normalizeTrimmedString(user?.displayName),
    timezone: normalizeTrimmedString(user?.timezone, "Asia/Shanghai"),
    locale: normalizeTrimmedString(user?.locale, "en-US"),
    profilePreferences: normalizeTrimmedString(user?.profilePreferences),
  };
}

export function createProfileUpdatePayload(
  user: Pick<UserDTO, "displayName" | "username" | "email" | "externalProfile"> | null | undefined,
  draft: ProfileDraft,
): PatchMePayload {
  const payload: PatchMePayload = {
    avatarURL: draft.avatarUrl,
    timezone: draft.timezone,
    locale: draft.locale,
    profilePreferences: draft.profilePreferences,
  };
  if (user?.externalProfile?.provider !== "sub2api") {
    payload.displayName = draft.displayName;
  }
  return payload;
}

export function isProfileDraftEqual(left: ProfileDraft, right: ProfileDraft): boolean {
  return (
    left.avatarUrl === right.avatarUrl &&
    left.displayName === right.displayName &&
    left.timezone === right.timezone &&
    left.locale === right.locale &&
    left.profilePreferences === right.profilePreferences
  );
}
