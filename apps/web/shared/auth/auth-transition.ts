// Only a non-secret policy flag survives a reload. Credentials never enter this store.
export const ACCOUNT_SWITCH_STORAGE_KEY = "deeix-chat:account-switch";
type TransitionStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export function createAuthTransition(storage: () => TransitionStorage) {
  let initialized = false;
  let blocked = false;
  let isolated = false;
  let generation = 0;
  let queue: Promise<unknown> = Promise.resolve();
  const listeners = new Set<() => void>();
  function initialize() {
    if (initialized) return;
    initialized = true;
    try {
      const target = storage();
      const probe = `${ACCOUNT_SWITCH_STORAGE_KEY}:probe`;
      target.setItem(probe, "1");
      target.removeItem(probe);
      const value = target.getItem(ACCOUNT_SWITCH_STORAGE_KEY);
      blocked = value !== null && value !== "active";
      isolated = value !== null;
    } catch {
      // We cannot distinguish a fresh tab from a failed switch before reload.
      blocked = isolated = true;
    }
  }
  function persist(value: "blocked" | "active") {
    try { storage().setItem(ACCOUNT_SWITCH_STORAGE_KEY, value); } catch { /* memory remains fail-closed */ }
  }
  return {
    blocked() { initialize(); return blocked; },
    acceptsPeers() { initialize(); return !isolated && !blocked; },
    generation() { return generation; },
    isCurrent(value: number) { return generation === value; },
    beginSwitch() {
      initialize();
      generation += 1;
      blocked = isolated = true;
      persist("blocked");
      for (const listener of listeners) listener();
      return generation;
    },
    beginSignIn() { initialize(); generation += 1; return generation; },
    complete(value: number) {
      initialize();
      if (generation !== value) return false;
      // Keep rejecting snapshots from old tabs after a successful switch.
      if (isolated) persist("active");
      blocked = false;
      for (const listener of listeners) listener();
      return true;
    },
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
    run<T>(task: () => Promise<T>): Promise<T> {
      const result = queue.then(task);
      queue = result.catch(() => undefined);
      return result;
    },
  };
}

export const authTransition = createAuthTransition(() => window.sessionStorage);

export class SupersededAuthenticationError extends Error {
  constructor() { super("Authentication was superseded"); }
}
export function assertCurrentAuthentication(generation: number): void {
  if (!authTransition.isCurrent(generation)) throw new SupersededAuthenticationError();
}
