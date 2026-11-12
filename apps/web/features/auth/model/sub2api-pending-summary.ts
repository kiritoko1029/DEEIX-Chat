import type { Sub2ApiImportSummary } from "../../../shared/api/auth-types.ts";

// Memory-only handoff. No remote JWT/API Key is kept, and a summary can only be
// shown after the exact local second-factor challenge it belongs to succeeds.
let pending: { challenge: string; summary: Sub2ApiImportSummary } | null = null;

export function clearPendingSub2ApiSummary(): void {
  pending = null;
}

export function rememberPendingSub2ApiSummary(challenge: string, summary: Sub2ApiImportSummary | null): void {
  pending = challenge && summary ? { challenge, summary } : null;
}

export function takePendingSub2ApiSummary(challenge: string): Sub2ApiImportSummary | null {
  const previous = pending;
  pending = null;
  return previous && previous.challenge === challenge ? previous.summary : null;
}
