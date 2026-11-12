import { appendFileSync, readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

// Only values validated here reach image names, workflow outputs or shell arguments.
export function imageReleasePlan(env, version) {
  if (!['push', 'workflow_dispatch'].includes(env.GITHUB_EVENT_NAME)) {
    throw new Error('Image publishing only supports tag pushes or tag dispatches.');
  }
  const match = /^refs\/tags\/v((?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?)$/.exec(env.GITHUB_REF ?? '');
  if (!match || match[1] !== version || version.length > 110) {
    throw new Error('Expected a Docker-compatible v<VERSION> tag matching VERSION exactly.');
  }
  if (!/^[a-f0-9]{40}$/.test(env.GITHUB_SHA ?? '')) throw new Error('Invalid source SHA.');
  if (!/^\d+$/.test(env.GITHUB_RUN_ID ?? '') || !/^\d+$/.test(env.GITHUB_RUN_ATTEMPT ?? '')) {
    throw new Error('Invalid workflow run identity.');
  }
  const repository = (env.GITHUB_REPOSITORY ?? '').toLowerCase();
  if (!/^[a-z0-9][a-z0-9_.-]*\/[a-z0-9][a-z0-9_.-]*$/.test(repository)) {
    throw new Error('Invalid GitHub repository.');
  }
  // The login account may differ from the image namespace. Never infer the target from secrets.
  if (!/^[a-z0-9][a-z0-9_-]*\/[a-z0-9]+(?:[._-][a-z0-9]+)*$/.test(env.DOCKERHUB_IMAGE ?? '')) {
    throw new Error('Set repository variable DOCKERHUB_IMAGE to namespace/repository (for this fork: 195658/deeix-chat).');
  }
  if (!env.DOCKERHUB_USERNAME?.trim() || !env.DOCKERHUB_TOKEN?.trim()) {
    throw new Error('Docker Hub was NOT published: both DOCKERHUB_USERNAME and DOCKERHUB_TOKEN secrets are required.');
  }
  const images = [`ghcr.io/${repository}`, `docker.io/${env.DOCKERHUB_IMAGE}`];
  // Include the run identity: rebuilding one commit (e.g. refreshed catalog data)
  // must not overwrite the earlier build's SHA-qualified reference.
  const tags = [`v${version}`, version, `sha-${env.GITHUB_SHA}-${env.GITHUB_RUN_ID}-${env.GITHUB_RUN_ATTEMPT}`];
  // Dispatch permits rebuilding an existing tag, but never promotes arbitrary refs to latest.
  if (env.GITHUB_EVENT_NAME === 'push' && !version.includes('-')) tags.push('latest');
  return { images, tags, staging: `run-${env.GITHUB_RUN_ID}-${env.GITHUB_RUN_ATTEMPT}`, version };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const plan = imageReleasePlan(process.env, readFileSync('VERSION', 'utf8').trim());
    appendFileSync(process.env.GITHUB_OUTPUT, `plan=${JSON.stringify(plan)}\n`);
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, `Image release validated: v${plan.version}. Docker Hub publication is pending both native builds and manifest verification.\n`);
  } catch (error) {
    if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `Docker Hub was NOT published: ${error.message}\n`);
    console.error(error.message);
    process.exitCode = 1;
  }
}
