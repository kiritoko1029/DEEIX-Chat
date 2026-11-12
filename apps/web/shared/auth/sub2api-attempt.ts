import type { Sub2ApiProtocolSelection } from "../api/auth-types.ts";
import type { Sub2ApiCredentials } from "./sub2api-link.ts";

export const SUB2API_ATTEMPT_TTL_MS = 5 * 60 * 1000;

export type Sub2ApiAttemptCloseReason = "succeeded" | "cancelled" | "failed" | "expired" | "superseded";

export function createSub2ApiAttempt(
  id: number,
  input: Sub2ApiCredentials,
  now: () => number = Date.now,
) {
  const expiresAt = now() + SUB2API_ATTEMPT_TTL_MS;
  let credentials: Sub2ApiCredentials | null = { userID: input.userID, token: input.token };
  let selection: Sub2ApiProtocolSelection | null = null;
  let closeReason: Sub2ApiAttemptCloseReason | null = null;

  function close(reason: Sub2ApiAttemptCloseReason): void {
    credentials = null;
    selection = null;
    closeReason = reason;
  }

  function expireIfNeeded(): void {
    if (!closeReason && now() >= expiresAt) close("expired");
  }

  return {
    id,
    expiresAt,
    get closeReason() {
      expireIfNeeded();
      return closeReason;
    },
    readCredentials(): Sub2ApiCredentials | null {
      expireIfNeeded();
      return credentials ? { userID: credentials.userID, token: credentials.token } : null;
    },
    requireProtocol(next: Sub2ApiProtocolSelection): boolean {
      expireIfNeeded();
      if (closeReason) return false;
      selection = next;
      return true;
    },
    readSelection(): Sub2ApiProtocolSelection | null {
      expireIfNeeded();
      return selection;
    },
    close,
  };
}

export type Sub2ApiAttempt = ReturnType<typeof createSub2ApiAttempt>;
