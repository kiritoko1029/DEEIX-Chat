"use client";

import { type AuthHost, type SessionStore, classifyAuthError, createAuthClient } from "@deeix/core";
import { authTransition, assertCurrentAuthentication, SupersededAuthenticationError } from "./auth-transition.ts";
import type { LoginData } from "@/shared/api/auth-types";
import { ApiError, apiRequest } from "@/shared/api/http-client";
import { clearSessionSnapshot, readAccessToken, readSessionRevision, writeSessionSnapshot } from "@/shared/auth/session";
import { isDesktopApp } from "@/shared/platform";
import { isShellSessionError, refreshSession } from "@/shared/platform/desktop-shell";

// Runtime host for the shared auth state machine (@deeix/core).
//
// Browser and desktop share one request path; only the refresh transport differs:
//   - browser: POST /auth/refresh with the HttpOnly cookie, server rotates it.
//   - desktop: the Tauri shell performs the refresh against the pinned server
//     using the keychain-held token. The webview never sees that token.

const AUTH_REFRESH_LOCK_NAME = "deeix-chat:auth-refresh";

const refreshGenerations = new WeakMap<object, number>();
const sessionStore: SessionStore = {
  readAccessToken,
  readRevision: readSessionRevision,
  write: (credentials) => {
    if (!authTransition.blocked() && refreshGenerations.get(credentials) === authTransition.generation()) {
      writeSessionSnapshot(credentials);
    }
  },
  // Refresh-driven clears never fan out to peers: each tab's own refresh
  // will fail on the same server state, and a peer may already hold a newer session.
  clear: () => clearSessionSnapshot({ syncPeers: false }),
};

const host: AuthHost = {
  store: sessionStore,
  async refreshSession() {
    const generation = authTransition.generation();
    return authTransition.run(async () => {
      if (authTransition.blocked()) throw new SupersededAuthenticationError();
      assertCurrentAuthentication(generation);
      let credentials: { accessToken: string; sessionID: string } | null;
      if (isDesktopApp()) {
        try { credentials = await refreshSession(); }
        catch (error) {
          if (isShellSessionError(error) && error.kind === "no_session") return null;
          throw error;
        }
      } else {
        const data = await apiRequest<LoginData>("/api/v1/auth/refresh", { method: "POST" });
        credentials = data.accessToken ? { accessToken: data.accessToken, sessionID: data.sessionID } : null;
      }
      assertCurrentAuthentication(generation);
      if (authTransition.blocked()) throw new SupersededAuthenticationError();
      if (credentials) refreshGenerations.set(credentials, generation);
      return credentials;
    });
  },
  classifyError(error) {
    if (error instanceof ApiError) {
      return classifyAuthError(error);
    }
    if (isShellSessionError(error) && error.kind === "http") {
      return classifyAuthError({ status: error.status, errorCode: error.errorCode });
    }
    return "other";
  },
  lock: {
    run(fn) {
      // lib.dom types `locks` as always present, but it is missing in insecure contexts and older browsers.
      const locks: LockManager | undefined = typeof navigator === "undefined" ? undefined : navigator.locks;
      return locks ? locks.request(AUTH_REFRESH_LOCK_NAME, fn) : fn();
    },
  },
};

const client = createAuthClient(host);
export const authClient: ReturnType<typeof createAuthClient> = {
  async refreshAccessToken(failedToken = "") {
    if (authTransition.blocked()) return "";
    const generation = authTransition.generation();
    try {
      const token = await client.refreshAccessToken(failedToken);
      return authTransition.isCurrent(generation) && !authTransition.blocked() ? token : "";
    } catch (error) {
      if (error instanceof SupersededAuthenticationError) return "";
      throw error;
    }
  },
  async recoverAccessToken(failedToken) {
    if (authTransition.blocked()) return "";
    const generation = authTransition.generation();
    const token = await client.recoverAccessToken(failedToken);
    return authTransition.isCurrent(generation) && !authTransition.blocked() ? token : "";
  },
  withAuthRetry(options) {
    const generation = authTransition.generation();
    return client.withAuthRetry({
      ...options,
      async execute(token) {
        assertCurrentAuthentication(generation);
        if (authTransition.blocked()) throw new SupersededAuthenticationError();
        const result = await options.execute(token);
        assertCurrentAuthentication(generation);
        if (authTransition.blocked()) throw new SupersededAuthenticationError();
        return result;
      },
    });
  },
};
