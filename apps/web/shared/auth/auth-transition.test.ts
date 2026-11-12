import assert from "node:assert/strict";
import test from "node:test";
import { ACCOUNT_SWITCH_STORAGE_KEY, createAuthTransition } from "./auth-transition.ts";

function memoryStorage() {
  const entries = new Map<string, string>();
  return { entries, removeItem: (key: string) => { entries.delete(key); }, getItem: (key: string) => entries.get(key) ?? null, setItem: (key: string, value: string) => { entries.set(key, value); } };
}

test("ordinary tabs keep automatic recovery; a switch survives reload without credentials", () => {
  const storage = memoryStorage();
  const state = createAuthTransition(() => storage);
  assert.equal(state.blocked(), false);
  assert.equal(state.acceptsPeers(), true);
  const id = state.beginSwitch();
  assert.equal(state.blocked(), true);
  assert.equal(state.acceptsPeers(), false);
  assert.deepEqual([...storage.entries], [[ACCOUNT_SWITCH_STORAGE_KEY, "blocked"]]);
  assert.equal(createAuthTransition(() => storage).blocked(), true);
  assert.equal(state.complete(id), true);
  assert.equal(state.blocked(), false);
  assert.equal(state.acceptsPeers(), false);
  assert.equal(createAuthTransition(() => storage).blocked(), false);
  assert.deepEqual([...storage.entries], [[ACCOUNT_SWITCH_STORAGE_KEY, "active"]]);
});

test("unavailable storage fails closed across reload, but explicit login can finish in memory", () => {
  const unavailable = () => { throw new Error("denied"); };
  const state = createAuthTransition(unavailable);
  assert.equal(state.blocked(), true);
  const id = state.beginSignIn();
  assert.equal(state.complete(id), true);
  assert.equal(state.blocked(), false);
  assert.equal(createAuthTransition(unavailable).blocked(), true);
});

test("stale exchange or login cannot release a newer switch barrier", () => {
  const state = createAuthTransition(memoryStorage);
  const first = state.beginSwitch();
  const second = state.beginSwitch();
  assert.equal(state.complete(first), false);
  assert.equal(state.blocked(), true);
  assert.equal(state.complete(second), true);
  const login = state.beginSignIn();
  state.beginSwitch();
  assert.equal(state.complete(login), false);
});

test("auth operations drain old responses before new exchange, including rejected operations", async () => {
  const state = createAuthTransition(memoryStorage);
  const events: string[] = [];
  let release!: () => void;
  const pending = new Promise<void>((resolve) => { release = resolve; });
  const refresh = state.run(async () => { events.push("old refresh starts"); await pending; events.push("old cookie response"); throw new Error("superseded"); });
  const exchange = state.run(async () => { events.push("new exchange"); });
  await Promise.resolve();
  assert.deepEqual(events, ["old refresh starts"]);
  release();
  await assert.rejects(refresh, /superseded/);
  await exchange;
  assert.deepEqual(events, ["old refresh starts", "old cookie response", "new exchange"]);
});

test("readable but unwritable storage also blocks automatic restoration", () => {
  const state = createAuthTransition(() => ({
    getItem: () => null,
    setItem: () => { throw new Error("quota exceeded"); },
    removeItem: () => {},
  }));
  assert.equal(state.blocked(), true);
  assert.equal(state.acceptsPeers(), false);
});
