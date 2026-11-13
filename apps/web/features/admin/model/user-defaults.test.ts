import assert from "node:assert/strict";
import test from "node:test";

import { DEFAULT_CREATE_USER_PAYLOAD } from "../types/users.ts";

test("new admin user drafts default to Asia/Shanghai", () => {
  assert.equal(DEFAULT_CREATE_USER_PAYLOAD.timezone, "Asia/Shanghai");
});
