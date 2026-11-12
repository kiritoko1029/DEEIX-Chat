"use client";

// Desktop session bootstrap: connects the shared session snapshot and the API
// client to the Tauri shell. No-op in browsers.

import { authTransition, assertCurrentAuthentication, SupersededAuthenticationError } from "@/shared/auth/auth-transition";
import type { LoginData } from "@/shared/api/auth-types";
import { registerRuntimeApiBaseURLResolver } from "@/shared/api/http-client";
import { registerSessionClearedHandler, writeSessionSnapshot } from "@/shared/auth/session";
import { isDesktopApp } from "@/shared/platform";
import { clearSession, leaveServer, localSignIn, type ServerInfo, storeSession } from "@/shared/platform/desktop-shell";
import { loadServer, readServerMode, readServerOrigin } from "@/shared/platform/server-address";

let initialized: Promise<ServerInfo | null> | null = null;

/**
 * Install desktop hooks and resolve with the configured server (null on first
 * run). Idempotent; browsers resolve immediately with null.
 */
export function initializeDesktopSession(): Promise<ServerInfo | null> {
  if (!isDesktopApp()) {
    return Promise.resolve(null);
  }
  initialized ??= (async () => {
    registerRuntimeApiBaseURLResolver(readServerOrigin);
    // Sign-out: remote tabs drop the keychain token and land on the login page.
    // Local tabs have no login page to come back through, so leaving the
    // server is the equivalent — the tab returns to the setup screen.
    registerSessionClearedHandler(async () => {
      if (readServerMode() === "local") {
        await leaveServer();
      } else {
        await clearSession();
      }
    });
    return loadServer();
  })();
  return initialized;
}

/**
 * Resolve once requests can be addressed: true right away in browsers; on
 * desktop true once this tab is bound to a server, false while it shows the
 * setup screen. Rejects when the bound server cannot be resolved.
 */
export async function waitForApiServer(): Promise<boolean> {
  if (!isDesktopApp()) {
    return true;
  }
  return (await initializeDesktopSession()) !== null;
}

/**
 * Record an authenticated session. Every sign-in path goes through here; on
 * desktop the refresh token is handed to the shell and never read back.
 */
// Called inside authTransition.run: shell writes and HTTP exchanges must share
// the same queue, so a superseded response cannot overwrite newer credentials.
export async function completeNativeSignIn(result: LoginData, generation: number): Promise<void> {
  assertCurrentAuthentication(generation);
  if (!result.accessToken || !result.sessionID || result.twoFactorRequired) throw new Error("Incomplete sign-in");
  if (isDesktopApp() && result.refreshToken) {
    await storeSession(result.refreshToken);
    if (!authTransition.isCurrent(generation)) {
      await clearSession();
      throw new SupersededAuthenticationError();
    }
  }
  assertCurrentAuthentication(generation);
  authTransition.complete(generation);
  writeSessionSnapshot({ accessToken: result.accessToken, sessionID: result.sessionID });
}

export async function clearNativeSessionForAccountSwitch(): Promise<void> {
  if (isDesktopApp()) await clearSession();
}

// Explicit credential / registration / provider / 2FA sign-in can release an
// isolation barrier. Refresh and peer snapshots never can.
export function authenticateExplicitly(request: () => Promise<LoginData>): Promise<LoginData> {
  const generation = authTransition.beginSignIn();
  return authTransition.run(async () => {
    assertCurrentAuthentication(generation);
    if (authTransition.blocked()) await clearNativeSessionForAccountSwitch();
    assertCurrentAuthentication(generation);
    const result = await request();
    assertCurrentAuthentication(generation);
    if (!result.twoFactorRequired) await completeNativeSignIn(result, generation);
    return result;
  });
}

/**
 * Local mode has no login form: obtain a session from the shell, which
 * refreshes the stored token or, failing that, redeems the sidecar's one-time
 * grant. Returns true when a session is now available.
 */
export async function ensureLocalSession(): Promise<boolean> {
  if (authTransition.blocked() || !isDesktopApp() || readServerMode() !== "local") {
    return false;
  }
  const generation = authTransition.generation();
  return authTransition.run(async () => {
    if (authTransition.blocked() || !authTransition.isCurrent(generation)) return false;
    const credentials = await localSignIn();
    await loadServer();
    if (authTransition.blocked() || !authTransition.isCurrent(generation)) {
      await clearSession();
      return false;
    }
    writeSessionSnapshot(credentials);
    return true;
  });
}
