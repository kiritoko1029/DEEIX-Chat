import { authTransition } from "./auth-transition.ts";
import { parseSub2ApiLink, type Sub2ApiLinkParseResult } from "./sub2api-link.ts";

export type CapturedSub2ApiBootstrap = Exclude<Sub2ApiLinkParseResult, { kind: "none" }>;

type Sub2ApiCaptureLocation = {
  pathname: string;
  search: string;
  hash: string;
};

type Sub2ApiCaptureHistory = {
  state: unknown;
  replaceState: (state: unknown, unused: string, url: string) => void;
};

let captured: CapturedSub2ApiBootstrap | null = null;
let attempt = 0;
const listeners = new Set<() => void>();
export function readSub2ApiAttempt(): number { return attempt; }
export function subscribeSub2ApiBootstrap(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

export function captureSub2ApiBootstrap(location: Sub2ApiCaptureLocation, history: Sub2ApiCaptureHistory): void {
  const parsed = parseSub2ApiLink(location);
  if (parsed.kind === "none") {
    return;
  }
  // Clean even malformed links, before exposing an attempt to React.
  // Pass a fresh state through the public History API. Reusing Next's internal
  // state makes its router skip the URL update and later restore the raw URL.
  history.replaceState(null, "", parsed.cleanedURL);
  captured = parsed;
  attempt = authTransition.beginSwitch();
  queueMicrotask(() => { for (const listener of listeners) listener(); });
}

export function hasSub2ApiBootstrap(): boolean {
  return captured !== null;
}

export function consumeSub2ApiBootstrap(): CapturedSub2ApiBootstrap | null {
  const current = captured;
  captured = null;
  return current;
}

let installed = false;
export function installSub2ApiCapture(): void {
  if (installed) return;
  installed = true;
  let cleaning = false;
  const capture = () => {
    if (cleaning) return;
    cleaning = true;
    try { captureSub2ApiBootstrap(window.location, window.history); }
    finally { cleaning = false; }
  };
  capture();
  window.addEventListener("hashchange", capture);
  window.addEventListener("popstate", capture);
  // Next wraps these methods too. Calling the previous implementation keeps
  // both wrappers intact and also covers client navigation without hashchange.
  for (const method of ["pushState", "replaceState"] as const) {
    const original = window.history[method].bind(window.history);
    window.history[method] = (...args: Parameters<History[typeof method]>) => {
      original(args[0], args[1], args[2]);
      capture();
    };
  }
}
