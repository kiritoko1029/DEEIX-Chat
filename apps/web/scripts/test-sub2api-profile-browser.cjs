/* Run after `pnpm --filter @deeix/web build`; all accounts and API responses are synthetic. */
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

const root = path.resolve(__dirname, '../out');
const mimeTypes = {
  '.css': 'text/css',
  '.html': 'text/html',
  '.js': 'text/javascript',
  '.json': 'application/json',
  '.svg': 'image/svg+xml',
  '.txt': 'text/x-component',
  '.woff2': 'font/woff2',
};
const server = http.createServer((request, response) => {
  const pathname = decodeURIComponent(new URL(request.url, 'http://test').pathname);
  let file = path.resolve(root, `.${pathname}`);
  if (!file.startsWith(root + path.sep) && file !== root) {
    response.writeHead(403);
    return response.end();
  }
  if (fs.existsSync(file) && fs.statSync(file).isDirectory()) {
    file = fs.existsSync(`${file}.html`) ? `${file}.html` : path.join(file, 'index.html');
  }
  if (!fs.existsSync(file) && fs.existsSync(`${file}.html`)) file += '.html';
  if (!fs.existsSync(file)) {
    response.writeHead(404);
    return response.end();
  }
  response.setHeader('Content-Type', mimeTypes[path.extname(file)] || 'application/octet-stream');
  fs.createReadStream(file).pipe(response);
});

function makeUser(overrides = {}) {
  return {
    id: 1,
    publicID: 'fake-public-1',
    username: 'local-user',
    displayName: 'Local User',
    role: 'user',
    status: 'active',
    timezone: 'Etc/UTC',
    locale: 'en-US',
    phone: '',
    email: 'security@example.test',
    emailSource: 'local',
    emailVerifiedAt: '2026-01-01T00:00:00Z',
    emailBootstrapUsedAt: null,
    avatarURL: '',
    profilePreferences: '',
    appearancePreferences: '',
    identityProviders: [],
    passwordEnabled: true,
    twoFactorEnabled: false,
    twoFactorRecoveryCount: 0,
    initialSecurityRequired: false,
    initialUsernameRequired: false,
    onboardingCompletedAt: '2026-01-01T00:00:00Z',
    usernameChangedAt: null,
    lastLoginAt: null,
    lastActiveAt: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    subscriptionTier: 'free',
    subscriptionStatus: '',
    subscriptionPlanName: '',
    subscriptionExpiresAt: null,
    billingBalanceUSD: 0,
    billingAccountStatus: 'active',
    ...overrides,
  };
}

async function main() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}),
  });

  async function scenario(name, initialViewer, initialAdminUsers, run) {
    const context = await browser.newContext();
    let viewer = initialViewer;
    const adminUsers = initialAdminUsers.map(user => ({ ...user }));
    const calls = [];
    const pageErrors = [];
    try {
      await context.addCookies([{
        name: 'refresh_token',
        value: 'FAKE_REFRESH_COOKIE',
        domain: '127.0.0.1',
        path: '/api/v1/auth',
        httpOnly: true,
        sameSite: 'Lax',
      }]);
      const page = await context.newPage();
      page.on('pageerror', error => pageErrors.push(error.message));
      await page.route('**/api/v1/**', async route => {
        const request = route.request();
        const url = new URL(request.url());
        const endpoint = url.pathname;
        let body = null;
        try { body = request.postDataJSON(); } catch {}
        calls.push({ endpoint, method: request.method(), url: request.url(), body });
        let data = {};

        if (endpoint === '/api/v1/auth/refresh') {
          data = { accessToken: 'FAKE_ACCESS', sessionID: 'FAKE_SESSION', user: viewer };
        } else if (endpoint === '/api/v1/me/username') {
          viewer = { ...viewer, ...body, usernameChangedAt: '2026-11-13T00:00:00Z' };
          data = { user: viewer };
        } else if (endpoint === '/api/v1/me' && request.method() === 'PATCH') {
          viewer = { ...viewer, ...body };
          delete viewer.externalProfile;
          data = { user: viewer };
        } else if (endpoint === '/api/v1/me') {
          data = { user: viewer };
        } else if (endpoint === '/api/v1/admin/users' && request.method() === 'POST') {
          data = { user: makeUser({ ...body, id: 999, publicID: 'fake-public-999' }) };
        } else if (endpoint === '/api/v1/admin/users') {
          const query = (url.searchParams.get('q') || '').toLowerCase();
          const results = query
            ? adminUsers.filter(user => [
              user.username,
              user.displayName,
              user.email,
              user.externalProfile?.username,
              user.externalProfile?.email,
            ].some(value => (value || '').toLowerCase().includes(query)))
            : adminUsers;
          data = { results, total: results.length };
        } else if (/^\/api\/v1\/admin\/users\/\d+$/.test(endpoint) && request.method() === 'PATCH') {
          const id = Number(endpoint.split('/').pop());
          const index = adminUsers.findIndex(user => user.id === id);
          const updated = { ...adminUsers[index], ...body };
          adminUsers[index] = updated;
          const responseUser = { ...updated };
          delete responseUser.externalProfile;
          data = { user: responseUser };
        } else if (endpoint === '/api/v1/admin/billing/config') {
          data = { config: { mode: 'self', displayCurrency: 'USD', usdToCNYRate: 7 } };
        } else if (endpoint === '/api/v1/admin/billing/plans') {
          data = [];
        } else if (endpoint === '/api/v1/auth/login-options') {
          data = {
            usernameEnabled: true,
            emailEnabled: true,
            emailVerificationEnabled: false,
            providers: [],
            providerAuthBridge: { enabled: false },
          };
        } else if (endpoint === '/api/v1/conversation-projects' || endpoint === '/api/v1/announcements' || endpoint === '/api/v1/models') {
          data = [];
        } else if (endpoint === '/api/v1/conversations') {
          data = { results: [], total: 0 };
        }

        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data }) });
      });
      await run({ page, calls, origin });
      assert.deepEqual(pageErrors, [], 'browser page errors');
      console.log(`PASS ${name}`);
    } finally {
      await context.close();
    }
  }

  const admin = makeUser({ id: 1, role: 'superadmin', username: 'root-admin', displayName: 'Root Admin' });
  const sourceUser = makeUser({
    id: 101,
    publicID: 'fake-public-101',
    username: 'sub2api_internal_101',
    displayName: 'sub2api user',
    email: 'internal-security@example.test',
    externalProfile: {
      provider: 'sub2api',
      username: 'source-user',
      displayName: 'Source Display',
      email: 'source@example.test',
      remoteID: 9001,
      profileSyncedAt: '2026-11-13T01:02:03Z',
    },
  });
  const pendingUser = makeUser({
    id: 102,
    publicID: 'fake-public-102',
    username: 'sub2api_internal_102',
    displayName: 'sub2api user',
    email: '',
    externalProfile: {
      provider: 'sub2api',
      username: '',
      displayName: '',
      email: '',
      remoteID: 9002,
      profileSyncedAt: null,
    },
  });
  const localMember = makeUser({
    id: 103,
    publicID: 'fake-public-103',
    username: 'local-member',
    displayName: 'Local Member',
    email: 'local@example.test',
  });

  try {
    await scenario('admin source identity, update preservation, search, and create timezone', admin, [sourceUser, pendingUser, localMember], async ({ page, calls, origin }) => {
      await page.goto(`${origin}/admin/users`);
      const sourceRow = page.locator('tr[data-user-id="101"]');
      const pendingRow = page.locator('tr[data-user-id="102"]');
      const localRow = page.locator('tr[data-user-id="103"]');
      await sourceRow.waitFor();
      const sourceText = await sourceRow.innerText();
      assert.match(sourceText, /source-user/);
      assert.match(sourceText, /source@example\.test/);
      assert.match(sourceText, /sub2api/);
      assert.equal(sourceText.includes('sub2api_internal_101'), false);
      assert.equal(sourceText.includes('internal-security@example.test'), false);
      assert.equal(await sourceRow.locator('[data-slot="avatar-fallback"]').innerText(), 'S');

      const pendingText = await pendingRow.innerText();
      assert.match(pendingText, /sub2api account #9002/);
      assert.match(pendingText, /Pending sync/);
      assert.equal(pendingText.includes('sub2api_internal_102'), false);
      const localText = await localRow.innerText();
      assert.match(localText, /Local Member/);
      assert.match(localText, /@local-member/);
      assert.match(localText, /local@example\.test/);
      assert.equal(await localRow.getAttribute('data-user-source'), 'local');

      const searchResponse = page.waitForResponse(response => response.url().includes('/admin/users?') && new URL(response.url()).searchParams.get('q') === 'source@example.test');
      await page.getByPlaceholder('Search username, display name, email, or public ID').fill('source@example.test');
      await searchResponse;
      assert(calls.some(call => call.endpoint === '/api/v1/admin/users' && new URL(call.url).searchParams.get('q') === 'source@example.test'));

      const roleResponse = page.waitForResponse(response => response.url().endsWith('/admin/users/101') && response.request().method() === 'PATCH');
      await sourceRow.getByRole('combobox').first().click();
      await page.getByRole('option', { name: 'admin', exact: true }).click();
      await roleResponse;
      await page.getByText('Role updated', { exact: true }).last().waitFor();
      assert.match(await sourceRow.innerText(), /source-user/);
      assert.match(await sourceRow.innerText(), /source@example\.test/);

      await sourceRow.getByRole('button', { name: /View or edit user/ }).click();
      const sheet = page.getByRole('dialog');
      const external = sheet.locator('[data-external-profile="sub2api"]');
      await external.waitFor();
      const externalText = await external.innerText();
      assert.match(externalText, /source-user/);
      assert.match(externalText, /source@example\.test/);
      assert.match(externalText, /9001/);
      assert.match(externalText, /read-only/);
      assert.equal(await external.locator('input').count(), 0);
      await sheet.getByText('Local security email', { exact: true }).waitFor();
      const displayInput = sheet.getByText('Local display name', { exact: true }).locator('..').locator('input');
      await displayInput.fill('Local renamed');
      const editResponse = page.waitForResponse(response => response.url().endsWith('/admin/users/101') && response.request().method() === 'PATCH');
      await sheet.getByRole('button', { name: 'Save', exact: true }).click();
      await editResponse;
      await sheet.waitFor({ state: 'hidden' });
      assert.match(await sourceRow.innerText(), /source-user/);
      assert.match(await sourceRow.innerText(), /source@example\.test/);
      const editCall = calls.filter(call => call.endpoint === '/api/v1/admin/users/101' && call.method === 'PATCH').at(-1);
      assert.equal(editCall.body.displayName, 'Local renamed');
      assert.equal('email' in editCall.body, false);
      assert.equal('username' in editCall.body, false);

      await page.getByRole('button', { name: 'Create', exact: true }).click();
      const createDialog = page.getByRole('dialog');
      await createDialog.locator('input').nth(0).fill('new-user');
      await createDialog.locator('input').nth(1).fill('New User');
      await createDialog.locator('input[type="password"]').fill('FakePassphrase123!');
      const createResponse = page.waitForResponse(response => response.url().endsWith('/admin/users') && response.request().method() === 'POST');
      await createDialog.getByRole('button', { name: 'Create', exact: true }).click();
      await createResponse;
      const createCall = calls.find(call => call.endpoint === '/api/v1/admin/users' && call.method === 'POST');
      assert.equal(createCall.body.timezone, 'Asia/Shanghai');
      assert.equal(createCall.body.username, 'new-user');
    });

    await scenario('sub2api profile is read-only and settings saves never write identity', sourceUser, [], async ({ page, calls, origin }) => {
      await page.goto(`${origin}/settings/general`);
      const accountInput = page.locator('#settings-external-account');
      const displayInput = page.locator('#settings-external-display-name');
      await accountInput.waitFor();
      assert.equal(await accountInput.inputValue(), 'source@example.test');
      assert.equal(await displayInput.inputValue(), 'source-user');
      assert.equal(await accountInput.evaluate(input => input.readOnly), true);
      assert.equal(await displayInput.evaluate(input => input.readOnly), true);
      assert.match(await page.locator('[data-external-profile="sub2api"]').innerText(), /read-only/);
      const inputValues = await page.locator('input').evaluateAll(inputs => inputs.map(input => input.value));
      assert.equal(inputValues.includes('sub2api_internal_101'), false);
      assert.equal(inputValues.includes('internal-security@example.test'), false);
      assert.match(await page.locator('#settings-timezone').innerText(), /Etc\/UTC/);

      await page.locator('#settings-timezone').click();
      await page.locator('input[role="combobox"]').last().fill('Asia/Shanghai');
      await page.getByRole('option', { name: 'Asia/Shanghai', exact: true }).click();
      const timezoneResponse = page.waitForResponse(response => response.url().endsWith('/me') && response.request().method() === 'PATCH');
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      await timezoneResponse;
      await page.getByText('Profile updated', { exact: true }).last().waitFor();
      const timezoneCall = calls.filter(call => call.endpoint === '/api/v1/me' && call.method === 'PATCH').at(-1);
      assert.equal(timezoneCall.body.timezone, 'Asia/Shanghai');
      assert.equal('displayName' in timezoneCall.body, false);
      assert.equal('username' in timezoneCall.body, false);
      assert.equal('email' in timezoneCall.body, false);
      assert.equal(JSON.stringify(timezoneCall.body).includes('source@example.test'), false);
      assert.equal(await accountInput.inputValue(), 'source@example.test');
      assert.equal(await displayInput.inputValue(), 'source-user');

      await accountInput.locator('..').locator('..').getByRole('button').first().click();
      const avatarDialog = page.getByRole('dialog');
      await avatarDialog.locator('input[placeholder="https://example.com/avatar.png"]').fill('generated:github:7');
      const avatarResponse = page.waitForResponse(response => response.url().endsWith('/me') && response.request().method() === 'PATCH');
      await avatarDialog.getByRole('button', { name: 'Apply', exact: true }).click();
      await avatarResponse;
      const avatarCall = calls.filter(call => call.endpoint === '/api/v1/me' && call.method === 'PATCH').at(-1);
      assert.deepEqual(avatarCall.body, { avatarURL: 'generated:github:7' });
      assert.equal(await accountInput.inputValue(), 'source@example.test');
      assert.equal(await displayInput.inputValue(), 'source-user');
      assert.equal(calls.filter(call => call.endpoint === '/api/v1/me/username').length, 0);
    });

    await scenario('ordinary profile retains one-time username and display-name editing', makeUser({ usernameChangedAt: null }), [], async ({ page, calls, origin }) => {
      await page.goto(`${origin}/settings/general`);
      const usernameInput = page.locator('input').nth(0);
      await usernameInput.waitFor();
      assert.equal(await page.locator('#settings-external-account').count(), 0);
      await usernameInput.fill('renamed-user');
      await page.locator('input').nth(1).fill('Renamed Local');
      const usernameResponse = page.waitForResponse(response => response.url().endsWith('/me/username') && response.request().method() === 'PATCH');
      const profileResponse = page.waitForResponse(response => response.url().endsWith('/me') && response.request().method() === 'PATCH');
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      await Promise.all([usernameResponse, profileResponse]);
      assert.deepEqual(calls.find(call => call.endpoint === '/api/v1/me/username').body, { username: 'renamed-user' });
      const profileCall = calls.find(call => call.endpoint === '/api/v1/me' && call.method === 'PATCH');
      assert.equal(profileCall.body.displayName, 'Renamed Local');
      assert.equal(profileCall.body.timezone, 'Etc/UTC');
    });
  } finally {
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
}

main().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
