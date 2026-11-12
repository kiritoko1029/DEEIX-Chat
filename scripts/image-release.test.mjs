import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { imageReleasePlan } from './image-release.mjs';

const base = {
  GITHUB_EVENT_NAME: 'push', GITHUB_REF: 'refs/tags/v2026.11.12',
  GITHUB_SHA: 'a'.repeat(40), GITHUB_RUN_ID: '123', GITHUB_RUN_ATTEMPT: '1',
  GITHUB_REPOSITORY: 'kiritoko1029/DEEIX-Chat', DOCKERHUB_IMAGE: '195658/deeix-chat',
  DOCKERHUB_USERNAME: 'different-login-account', DOCKERHUB_TOKEN: 'test-only-placeholder',
};
test('stable CalVer publishes exact versions, full commit SHA and latest to explicit namespace', () => {
  const plan = imageReleasePlan(base, '2026.11.12');
  assert.deepEqual(plan.images, ['ghcr.io/kiritoko1029/deeix-chat', 'docker.io/195658/deeix-chat']);
  assert.deepEqual(plan.tags, ['v2026.11.12', '2026.11.12', `sha-${'a'.repeat(40)}-123-1`, 'latest']);
  assert.equal(plan.staging, 'run-123-1');
});
test('prerelease and manual tag dispatch never promote latest', () => {
  assert.ok(!imageReleasePlan({ ...base, GITHUB_REF: 'refs/tags/v2026.11.12-rc.1' }, '2026.11.12-rc.1').tags.includes('latest'));
  assert.ok(!imageReleasePlan({ ...base, GITHUB_EVENT_NAME: 'workflow_dispatch' }, '2026.11.12').tags.includes('latest'));
});
test('reject branches, PRs, mismatches, missing credentials and injected values', () => {
  for (const override of [
    { GITHUB_REF: 'refs/heads/main' }, { GITHUB_REF: 'refs/heads/sub2api-2026-11-12' },
    { GITHUB_EVENT_NAME: 'pull_request' }, { GITHUB_REF: 'refs/tags/v2026.11.13' },
    { GITHUB_REF: 'refs/tags/v2026.11.12;id' }, { DOCKERHUB_IMAGE: '195658/deeix-chat\nother/image' },
    { DOCKERHUB_IMAGE: '' }, { DOCKERHUB_USERNAME: '' }, { DOCKERHUB_TOKEN: '' },
    { GITHUB_SHA: '$(id)' }, { GITHUB_RUN_ID: '1;id' }, { GITHUB_REPOSITORY: 'owner/repo\n' },
  ]) assert.throws(() => imageReleasePlan({ ...base, ...override }, '2026.11.12'));
});
test('registry plans stay job-local when a secret username matches the public namespace', () => {
  const plan = imageReleasePlan({ ...base, DOCKERHUB_USERNAME: '195658' }, '2026.11.12');
  assert.ok(plan.images.includes('docker.io/195658/deeix-chat'));
  const workflow = readFileSync(new URL('../.github/workflows/image.yml', import.meta.url), 'utf8');
  assert.ok(!workflow.includes('needs.validate.outputs.plan'));
  assert.equal((workflow.match(/run: node scripts\/image-release\.mjs/g) ?? []).length, 3);
  assert.ok(workflow.includes('fromJSON(steps.release.outputs.plan).version'));
  assert.ok(!/^    outputs:/m.test(workflow));
});

test('normal SemVer remains supported; retry staging does not mix architectures from different attempts', () => {
  assert.ok(imageReleasePlan({ ...base, GITHUB_REF: 'refs/tags/v0.4.7' }, '0.4.7').tags.includes('latest'));
  assert.equal(imageReleasePlan({ ...base, GITHUB_RUN_ATTEMPT: '2' }, '2026.11.12').staging, 'run-123-2');
  assert.notDeepEqual(imageReleasePlan({ ...base, GITHUB_RUN_ATTEMPT: '2' }, '2026.11.12').tags, imageReleasePlan(base, '2026.11.12').tags);
});
