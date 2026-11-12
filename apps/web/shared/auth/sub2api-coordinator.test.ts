import assert from "node:assert/strict";
import test from "node:test";

import { createSub2ApiBootstrapCoordinator } from "./sub2api-coordinator.ts";

test("shares one in-flight exchange across Strict Mode effect replays", async () => {
  let calls = 0;
  const coordinator = createSub2ApiBootstrapCoordinator(async () => {
    calls += 1;
    await Promise.resolve();
    return "signed-in";
  });

  assert.equal(coordinator.started, false);
  const first = coordinator.run();
  const replay = coordinator.run();
  assert.equal(first, replay);
  assert.equal(coordinator.started, true);
  assert.equal(await first, "signed-in");
  assert.equal(await replay, "signed-in");
  assert.equal(calls, 1);
});

test("does not run the exchange again after completion", async () => {
  let calls = 0;
  const coordinator = createSub2ApiBootstrapCoordinator(async () => {
    calls += 1;
    return calls;
  });

  assert.equal(await coordinator.run(), 1);
  assert.equal(await coordinator.run(), 1);
  assert.equal(calls, 1);
});

test("retains a rejected result instead of automatically retrying", async () => {
  let calls = 0;
  const coordinator = createSub2ApiBootstrapCoordinator(async () => {
    calls += 1;
    throw new Error("exchange failed");
  });

  const first = coordinator.run();
  const replay = coordinator.run();
  assert.equal(first, replay);
  await assert.rejects(first, /exchange failed/);
  await assert.rejects(coordinator.run(), /exchange failed/);
  assert.equal(calls, 1);
});
