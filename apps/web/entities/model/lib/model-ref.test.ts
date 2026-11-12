import assert from "node:assert/strict";
import test from "node:test";

import { findExactModelRef, parsePersonalModelRef } from "./model-ref.ts";

const first = "personal:provider001/gpt-4o";
const second = "personal:provider002/gpt-4o";

test("same upstream model names from different Keys keep independent provider references", () => {
  assert.deepEqual(parsePersonalModelRef(first), { providerID: "provider001", modelName: "gpt-4o" });
  assert.deepEqual(parsePersonalModelRef(second), { providerID: "provider002", modelName: "gpt-4o" });
  assert.notEqual(first, second);
});

test("a remembered provider-specific selection is preserved when another same-named model exists", () => {
  const models = [{ platformModelName: first }, { platformModelName: second }];
  assert.equal(findExactModelRef(models, second), second);
  assert.equal(findExactModelRef(models, `  ${second}  `), second);
});

test("an unavailable preference does not fail over to another provider with the same model name", () => {
  const models = [{ platformModelName: first }, { platformModelName: second }];
  assert.equal(findExactModelRef(models, "personal:provider003/gpt-4o"), "");
  assert.equal(findExactModelRef(models, "gpt-4o"), "");
});
