/* Run after `pnpm --filter @deeix/web build`.
 * Supply PLAYWRIGHT_MODULE (installed playwright or playwright-core) and optionally
 * CHROME_EXECUTABLE; no real backend, account, or credential is used.
 */
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = path.resolve(__dirname, '../out');
const barrierKey = 'deeix-chat:account-switch';
const server = http.createServer((request, response) => {
  let file = path.join(root, decodeURIComponent(new URL(request.url, 'http://test').pathname));
  if (!file.startsWith(root + path.sep) && file !== root) { response.writeHead(403); return response.end(); }
  if (fs.existsSync(file) && fs.statSync(file).isDirectory()) file = fs.existsSync(`${file}.html`) ? `${file}.html` : path.join(file, 'index.html');
  if (!fs.existsSync(file) && fs.existsSync(`${file}.html`)) file += '.html';
  if (!fs.existsSync(file)) { response.writeHead(404); return response.end(); }
  response.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.txt': 'text/x-component', '.json': 'application/json', '.svg': 'image/svg+xml' })[path.extname(file)] || 'application/octet-stream');
  fs.createReadStream(file).pipe(response);
});
const user = { id: 2, publicID: 'fake-user-2', username: 'fake-new-user', displayName: 'Fake new user', role: 'user', status: 'active', timezone: 'UTC', locale: 'en-US', phone: '', email: '', avatarURL: '', profilePreferences: '{}', appearancePreferences: '{}', identityProviders: [], initialSecurityRequired: false, initialUsernameRequired: false, onboardingCompletedAt: '2026-01-01T00:00:00Z' };
const session = (name = 'NEW') => ({ accessToken: `FAKE_${name}_ACCESS`, sessionID: `FAKE_${name}_SESSION`, user });
const challenge = { twoFactorRequired: true, twoFactorChallengeToken: 'FAKE_CHALLENGE', verificationMethods: ['two_factor'] };
const protocolList = ['openai_responses', 'anthropic_messages', 'openai_chat_completions'];
const protocolItem = (keyID, name = `Unknown Key ${keyID}`, platform = 'DeepSeek') => ({ keyID, name, groupName: 'Custom group', platform });
const protocolRequired = (route, items) => route.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({
  errorCode: 'auth.sub2api_protocol_required', errorMsg: 'FAKE_RAW_PROTOCOL_ERROR', details: { protocolSelection: { items, protocols: protocolList } },
}) });
const protocolTrigger = (page, keyID) => page.getByRole('combobox', { name: new RegExp(`#${keyID}`) });
const chooseProtocol = async (page, keyID, label) => {
  await protocolTrigger(page, keyID).click();
  await page.getByRole('option', { name: new RegExp(label) }).click();
};
const confirmProtocols = page => page.locator('[data-sub2api-protocol-confirm]');
const protocolPanel = async page => page.getByText(/Choose an API protocol|选择接口协议/, { exact: true }).waitFor();
const delay = (ms) => new Promise(resolve => setTimeout(resolve, ms));
async function main() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = process.env.SUB2API_TEST_ORIGIN || `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
  let passed = 0;
  async function scenario(name, options, run) {
    if (process.env.SUB2API_TEST_FILTER && !name.includes(process.env.SUB2API_TEST_FILTER)) return;
    const context = await browser.newContext();
    try {
      if (options.native) await context.addInitScript(({ clearFails, storeDelay }) => {
        window.isTauri = true;
        window.__fakeShellCalls = [];
        window.__fakeKeychain = 'OLD';
        window.__TAURI_EVENT_PLUGIN_INTERNALS__ = { unregisterListener: () => {} };
        window.__TAURI_INTERNALS__ = {
          metadata: { currentWindow: { label: 'test' }, currentWebview: { label: 'test' } },
          transformCallback: () => 1,
          unregisterCallback: () => {},
          invoke: async (command, args) => {
            window.__fakeShellCalls.push(command);
            if (command === 'get_server') return { mode: 'remote', origin: location.origin };
            if (command === 'clear_session') {
              if (clearFails) throw new Error('fake keychain failure');
              window.__fakeKeychain = '';
            }
            if (command === 'store_session') {
              if (storeDelay) await new Promise(resolve => setTimeout(resolve, storeDelay));
              window.__fakeKeychain = args.refreshToken;
            }
            if (command === 'refresh_session') return { accessToken: 'FAKE_OLD_ACCESS', sessionID: 'FAKE_OLD_SESSION' };
            if (command === 'get_distribution') return { updateMode: 'disabled', releasesUrl: '' };
            return 1;
          },
        };
      }, options.native);
      if (options.storageUnavailable) await context.addInitScript(() => {
        Object.defineProperty(window, 'sessionStorage', { get() { throw new DOMException('denied', 'SecurityError'); } });
      });
      await context.addCookies([{ name: 'refresh_token', value: 'FAKE_OLD_COOKIE', domain: '127.0.0.1', path: '/api/v1/auth', httpOnly: true, sameSite: 'Lax' }]);
      const page = await context.newPage();
      const calls = [];
      await page.route('**/api/v1/**', async route => {
        const request = route.request();
        const endpoint = new URL(request.url()).pathname;
        const call = { endpoint, authorization: request.headers().authorization || '', body: request.postDataJSON() };
        calls.push(call);
        let data = {};
        if (endpoint.endsWith('/sub2api/exchange')) {
          if (options.exchange) return options.exchange(route, call, calls);
          return route.fulfill({ status: 401, contentType: 'application/json', body: JSON.stringify({ errorMsg: 'fake rejection' }) });
        }
        if (endpoint.endsWith('/auth/refresh')) {
          if (options.refresh) return options.refresh(route, call, calls);
          data = session((request.headers().cookie || '').includes('FAKE_NEW_COOKIE') ? 'NEW' : 'OLD');
        } else if (endpoint.endsWith('/auth/login') || endpoint.endsWith('/auth/2fa/verify')) data = session();
        else if (endpoint.endsWith('/settings/login-page')) data = { defaultNextPath: options.next || '/chat' };
        else if (endpoint.endsWith('/auth/login-options')) data = { usernameEnabled: true, emailEnabled: true, emailVerificationEnabled: false, providers: [], providerAuthBridge: { enabled: false } };
        else if (endpoint.endsWith('/auth/sessions') || endpoint.endsWith('/me/identities')) data = { results: [], total: 0 };
        else if (endpoint.endsWith('/me/2fa')) data = { available: true, totpEnabled: false, required: false, recoveryCount: 0, enabledAt: null };
        else if (endpoint === '/api/v1/me') data = { user: options.viewer || user };
        else if (endpoint.includes('conversation-projects') || endpoint.includes('announcements') || endpoint.endsWith('/models')) data = [];
        else if (endpoint.includes('conversations')) data = { results: [], total: 0 };
        await route.fulfill({ status: 200, contentType: 'application/json', headers: data.accessToken && !endpoint.endsWith('/auth/refresh') ? { 'Set-Cookie': 'refresh_token=FAKE_NEW_COOKIE; Path=/api/v1/auth; HttpOnly; SameSite=Lax' } : {}, body: JSON.stringify({ data }) });
      });
      const exchanges = () => calls.filter(call => call.endpoint.endsWith('/sub2api/exchange'));
      const refreshes = () => calls.filter(call => call.endpoint.endsWith('/auth/refresh'));
      const checkClean = async () => {
        assert(!page.url().includes('token='), 'credential remains in URL');
        if (!options.storageUnavailable) {
          const stored = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage }, history: history.state }));
          assert(!stored.includes('FAKE_PANEL') && !stored.includes('FAKE_SECOND_PANEL'), 'panel credential persisted');
        }
      };
      await run({ page, context, calls, exchanges, refreshes, checkClean, origin });
      passed++;
      console.log(`PASS ${name}`);
    } finally { await context.close(); }
  }
  const fulfill = (route, data) => route.fulfill({ status: 200, contentType: 'application/json', headers: data.accessToken ? { 'Set-Cookie': 'refresh_token=FAKE_NEW_COOKIE; Path=/api/v1/auth; HttpOnly; SameSite=Lax' } : {}, body: JSON.stringify({ data }) });
  const failure = async page => page.getByRole('button', { name: /返回普通登录|Back to normal sign-in/ }).waitFor();
  const normalLogin = async page => {
    await page.locator('input[name=username]').fill('fake-user');
    await page.locator('input[name=password]').fill('FakePassword123!');
    await page.locator('button[type=submit]').click();
    await page.waitForURL('**/chat');
  };
  try {
    await scenario('ordinary root still restores the ordinary cookie session', {}, async ({ page, origin, exchanges, refreshes }) => {
      await page.goto(`${origin}/`); await page.waitForURL('**/chat');
      await page.waitForFunction(() => location.pathname === '/chat'); await delay(300);
      assert.equal(exchanges().length, 0); assert.equal(refreshes().length, 1);
    });
    for (const kind of ['rejected', 'network', 'malformed', 'storage-unavailable']) {
      await scenario(`${kind}: isolated on login, direct /chat, reload, and peer broadcast`, {
        storageUnavailable: kind === 'storage-unavailable',
        ...(kind === 'network' ? { exchange: route => route.abort('failed') } : {}),
      }, async ({ page, origin, refreshes, exchanges, checkClean, calls }) => {
        await page.goto(origin + (kind === 'malformed' ? '/?user_id=1' : '/#user_id=1&token=FAKE_PANEL_JWT'));
        await failure(page); await checkClean();
        await page.getByRole('button', { name: /返回普通登录|Back to normal sign-in/ }).click();
        await page.waitForURL('**/login');
        await page.evaluate(() => {
          const channel = new BroadcastChannel('deeix-chat:session-snapshot');
          channel.postMessage({ type: 'session_snapshot', snapshot: { accessToken: 'FAKE_PEER_OLD_ACCESS', sessionID: 'FAKE_PEER_OLD_SESSION' } });
          channel.close();
        });
        await delay(150);
        assert.equal(new URL(page.url()).pathname, '/login');
        await page.locator('input[name=username]').waitFor();
        await page.goto(`${origin}/chat`); await page.waitForURL('**/login?**');
        await page.reload(); await page.locator('input[name=username]').waitFor(); await delay(150);
        assert.equal(refreshes().length, 0);
        assert(!calls.some(call => /FAKE_(OLD|PEER_OLD)_ACCESS/.test(call.authorization)));
        assert.equal(exchanges().length, kind === 'malformed' ? 0 : 1);
        await normalLogin(page);
        assert(calls.some(call => call.endpoint.endsWith('/auth/login')));
        if (kind !== 'storage-unavailable') {
          assert.equal(await page.evaluate(key => sessionStorage.getItem(key), barrierKey), 'active');
          await page.reload(); await delay(300);
          assert(calls.some(call => call.authorization === 'Bearer FAKE_NEW_ACCESS'));
          assert(!calls.some(call => call.authorization === 'Bearer FAKE_OLD_ACCESS'));
        }
      });
    }
    await scenario('2FA remains isolated; successful verification with default / reaches /chat', { next: '/', exchange: route => fulfill(route, challenge) }, async ({ page, origin, refreshes, exchanges, checkClean }) => {
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`); await page.waitForURL('**/login');
      await page.locator('input[name=otp]').waitFor(); await checkClean(); assert.equal(refreshes().length, 0);
      await page.goto(`${origin}/chat`); await page.waitForURL('**/login?**');
      await page.goto(`${origin}/login`);
      await page.locator('input[name=otp]').fill('123456');
      await page.locator('button[type=submit]').click(); await page.waitForURL('**/chat');
      assert.equal(refreshes().length, 0); assert.equal(exchanges().length, 1);
      assert.equal(await page.evaluate(key => sessionStorage.getItem(key), barrierKey), 'active');
    });
    await scenario('same-document 2FA handoff ends before default root navigation', { next: '/', exchange: route => fulfill(route, challenge) }, async ({ page, origin, refreshes, exchanges }) => {
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`); await page.waitForURL('**/login');
      await page.locator('input[name=otp]').fill('123456');
      await page.locator('button[type=submit]').click(); await page.waitForURL('**/chat');
      assert.equal(refreshes().length, 0); assert.equal(exchanges().length, 1);
      assert.equal(await page.evaluate(key => sessionStorage.getItem(key), barrierKey), 'active');
    });
    await scenario('unavailable challenge storage keeps 2FA fail-closed', { storageUnavailable: true, exchange: route => fulfill(route, challenge) }, async ({ page, origin, refreshes, exchanges }) => {
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`); await failure(page);
      await page.goto(`${origin}/chat`); await page.waitForURL('**/login?**');
      assert.equal(refreshes().length, 0); assert.equal(exchanges().length, 1);
    });
    await scenario('protocol 409 requires an explicit Responses choice before sign-in', {
      exchange: (route, call) => call.body.protocolSelections ? fulfill(route, session()) : protocolRequired(route, [protocolItem(71, 'DeepSeek Key')]),
    }, async ({ page, origin, exchanges, checkClean }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`);
      await protocolPanel(page);
      assert.equal(await confirmProtocols(page).isDisabled(), true);
      await page.getByText('DeepSeek Key', { exact: false }).waitFor();
      await chooseProtocol(page, 71, 'OpenAI Responses.*\\/v1/responses');
      assert.equal(await confirmProtocols(page).isDisabled(), false);
      await confirmProtocols(page).click();
      await page.waitForURL('**/chat'); await checkClean();
      assert.equal(exchanges().length, 2);
      assert.deepEqual(Object.keys(exchanges()[0].body).sort(), ['token', 'userID']);
      assert.deepEqual(exchanges()[1].body.protocolSelections, [{ keyID: 71, protocol: 'openai_responses' }]);
      assert.deepEqual(exchanges()[1].body.skipKeyIDs, []);
      assert(!(await page.locator('body').innerText()).includes('FAKE_RAW_PROTOCOL_ERROR'));
    });
    await scenario('mixed protocol and user skip produce a precise partial summary', {
      exchange: (route, call) => call.body.protocolSelections ? fulfill(route, { ...session(), sub2apiImport: {
        total: 2, eligible: 1, imported: 1, reused: 0, skipped: 1, failed: 0,
        items: [
          { keyID: 71, name: 'DeepSeek Key', groupName: 'Custom group', status: 'imported' },
          { keyID: 72, name: 'Skip Key', groupName: 'Custom group', status: 'skipped', reason: 'user_skipped' },
        ],
      } }) : protocolRequired(route, [protocolItem(71, 'DeepSeek Key'), protocolItem(72, 'Skip Key')]),
    }, async ({ page, origin, exchanges, checkClean }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await chooseProtocol(page, 71, 'OpenAI Chat Completions.*\\/v1/chat/completions');
      await chooseProtocol(page, 72, 'Skip for this sign-in|本次跳过');
      await confirmProtocols(page).click(); await page.waitForURL('**/chat'); await checkClean();
      assert.deepEqual(exchanges()[1].body.protocolSelections, [{ keyID: 71, protocol: 'openai_chat_completions' }]);
      assert.deepEqual(exchanges()[1].body.skipKeyIDs, [72]);
      const summary = page.locator('[data-sub2api-import-summary]');
      await summary.waitFor(); await summary.locator('details summary').click();
      assert.match(await summary.innerText(), /Skipped by you|本次由你主动跳过/);
      assert.match(await summary.innerText(), /Skip Key/);
    });
    await scenario('double submission and a repeated 409 keep one in-flight continuation', {
      exchange: async (route, _call, calls) => {
        const count = calls.filter(item => item.endpoint.endsWith('/sub2api/exchange')).length;
        if (count === 1) return protocolRequired(route, [protocolItem(71)]);
        if (count === 2) { await delay(250); return protocolRequired(route, [protocolItem(72, 'Newly Pending Key')]); }
        return fulfill(route, session());
      },
    }, async ({ page, origin, exchanges }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await page.getByRole('button', { name: /Apply OpenAI Responses|应用 OpenAI Responses/ }).click();
      await confirmProtocols(page).evaluate(button => {
        button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      });
      await protocolTrigger(page, 72).waitFor();
      assert.equal(exchanges().length, 2);
      assert.deepEqual(exchanges()[1].body.protocolSelections, [{ keyID: 71, protocol: 'openai_responses' }]);
      await chooseProtocol(page, 72, 'Anthropic Messages.*\\/v1/messages');
      await confirmProtocols(page).click(); await page.waitForURL('**/chat');
      assert.equal(exchanges().length, 3);
      assert.deepEqual(exchanges()[2].body.protocolSelections, [
        { keyID: 71, protocol: 'openai_responses' },
        { keyID: 72, protocol: 'anthropic_messages' },
      ]);
    });
    await scenario('protocol selection preserves the 2FA challenge and pending summary handoff', {
      next: '/',
      exchange: (route, call) => call.body.protocolSelections ? fulfill(route, { ...challenge, sub2apiImport: {
        total: 1, eligible: 1, imported: 0, reused: 0, skipped: 1, failed: 0,
        items: [{ keyID: 71, name: '2FA Pending Key', groupName: 'Custom group', status: 'skipped', reason: 'two_factor_required' }],
      } }) : protocolRequired(route, [protocolItem(71, '2FA Pending Key')]),
    }, async ({ page, origin, exchanges, checkClean }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await chooseProtocol(page, 71, 'Anthropic Messages.*\\/v1/messages');
      await confirmProtocols(page).click(); await page.waitForURL('**/login'); await checkClean();
      await page.locator('input[name=otp]').waitFor();
      assert.equal(await page.locator('[data-sub2api-import-summary]').count(), 0);
      await page.locator('input[name=otp]').fill('123456');
      await page.locator('button[type=submit]').click(); await page.waitForURL('**/chat');
      assert.equal(exchanges().length, 2);
      const summary = page.locator('[data-sub2api-import-summary]');
      await summary.waitFor(); await summary.locator('details summary').click();
      assert.match(await summary.innerText(), /2FA Pending Key/);
    });
    await scenario('a new link synchronously blocks the previous protocol selection callback', {
      exchange: (route, call) => {
        if (call.body.userID === 2) return fulfill(route, session('NEW'));
        if (call.body.protocolSelections) return fulfill(route, session('STALE'));
        return protocolRequired(route, [protocolItem(71)]);
      },
    }, async ({ page, origin, calls, exchanges, checkClean }) => {
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await chooseProtocol(page, 71, 'OpenAI Responses.*\\/v1/responses');
      await page.evaluate(() => {
        history.pushState(null, '', '/#user_id=2&token=FAKE_SECOND_PANEL_JWT');
        document.querySelector('[data-sub2api-protocol-confirm]').click();
      });
      await page.waitForURL('**/chat'); await checkClean();
      assert.equal(exchanges().length, 2);
      assert.deepEqual(exchanges().map(call => call.body.userID), [1, 2]);
      assert.equal(exchanges().some(call => call.body.protocolSelections), false);
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_STALE_ACCESS'));
    });
    await scenario('a superseded in-flight protocol continuation cannot commit a stale session', {
      exchange: async (route, call) => {
        if (call.body.userID === 2) return fulfill(route, session('NEW'));
        if (call.body.protocolSelections) { await delay(350); return fulfill(route, session('STALE')); }
        return protocolRequired(route, [protocolItem(71)]);
      },
    }, async ({ page, origin, calls, exchanges, checkClean }) => {
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await chooseProtocol(page, 71, 'OpenAI Responses.*\\/v1/responses');
      const continued = page.waitForRequest(request => request.url().endsWith('/api/v1/auth/sub2api/exchange') && (request.postData() || '').includes('protocolSelections'));
      await confirmProtocols(page).click(); await continued;
      await page.goto(`${origin}/#user_id=2&token=FAKE_SECOND_PANEL_JWT`);
      await page.waitForURL('**/chat'); await checkClean();
      assert.equal(exchanges().length, 3);
      assert.deepEqual(exchanges().map(call => call.body.userID), [1, 1, 2]);
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_STALE_ACCESS'));
    });
    await scenario('cancelling protocol selection returns to isolated normal login without retrying', {
      exchange: route => protocolRequired(route, [protocolItem(71)]),
    }, async ({ page, origin, calls, exchanges, refreshes, checkClean }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await page.getByRole('button', { name: /Back to normal sign-in|返回普通登录/ }).click();
      await page.waitForURL('**/login'); await checkClean();
      assert.equal(exchanges().length, 1); assert.equal(refreshes().length, 0);
      assert.equal(await page.evaluate(key => sessionStorage.getItem(key), barrierKey), 'blocked');
      await normalLogin(page);
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_OLD_ACCESS'));
      assert.equal(exchanges().length, 1);
    });
    await scenario('a rejected continuation is terminal and never renders upstream text', {
      exchange: (route, call) => call.body.protocolSelections ? route.fulfill({
        status: 401, contentType: 'application/json', body: JSON.stringify({ errorCode: 'auth.sub2api_rejected', errorMsg: 'FAKE_EXPIRED_RAW_SECRET', details: { stage: 'token' } }),
      }) : protocolRequired(route, [protocolItem(71)]),
    }, async ({ page, origin, exchanges, checkClean }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await chooseProtocol(page, 71, 'OpenAI Responses.*\\/v1/responses');
      await confirmProtocols(page).click(); await failure(page); await checkClean();
      assert.equal(exchanges().length, 2);
      assert(!(await page.locator('body').innerText()).includes('FAKE_EXPIRED_RAW_SECRET'));
      assert.match(await page.locator('body').innerText(), /did not accept the login credential|未接受登录凭据/);
    });
    await scenario('an all-failed summary remains available after protocol selection', {
      exchange: (route, call) => call.body.protocolSelections ? route.fulfill({ status: 403, contentType: 'application/json', body: JSON.stringify({
        errorCode: 'auth.sub2api_import_failed', errorMsg: 'FAKE_RAW_PROTOCOL_ERROR', details: { sub2apiImport: {
          total: 1, eligible: 1, imported: 0, reused: 0, skipped: 0, failed: 1,
          items: [{ keyID: 71, name: 'Failed DeepSeek Key', groupName: 'Custom group', status: 'failed', reason: 'quota_exhausted' }],
        } },
      }) }) : protocolRequired(route, [protocolItem(71, 'Failed DeepSeek Key')]),
    }, async ({ page, origin }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await chooseProtocol(page, 71, 'OpenAI Responses.*\\/v1/responses');
      await confirmProtocols(page).click(); await failure(page);
      const summary = page.locator('[data-sub2api-import-summary]');
      await summary.locator('details summary').click();
      assert.match(await summary.innerText(), /Failed DeepSeek Key/);
      assert(!(await page.locator('body').innerText()).includes('FAKE_RAW_PROTOCOL_ERROR'));
    });
    await scenario('protocol selection expires after five minutes without another exchange', {
      exchange: route => protocolRequired(route, [protocolItem(71)]),
    }, async ({ page, origin, exchanges, refreshes, checkClean }) => {
      await page.clock.install();
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await page.clock.fastForward(300001);
      await failure(page); await checkClean();
      assert.match(await page.locator('body').innerText(), /expired|过期/);
      assert.equal(exchanges().length, 1); assert.equal(refreshes().length, 0);
      assert.equal(await page.evaluate(key => sessionStorage.getItem(key), barrierKey), 'blocked');
    });
    await scenario('leaving protocol continuation during native store cannot publish a cancelled session', {
      native: { storeDelay: 800 },
      exchange: (route, call) => call.body.protocolSelections
        ? fulfill(route, { ...session('CANCELLED'), refreshToken: 'FAKE_CANCELLED_REFRESH' })
        : protocolRequired(route, [protocolItem(71)]),
    }, async ({ page, origin, calls }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await chooseProtocol(page, 71, 'OpenAI Responses.*\\/v1/responses');
      await confirmProtocols(page).click();
      await page.waitForFunction(() => window.__fakeShellCalls.includes('store_session'));
      await page.evaluate(() => history.pushState(null, '', '/login'));
      await page.waitForFunction(() => window.__fakeShellCalls.filter(command => command === 'clear_session').length >= 2, null, { timeout: 5000 });
      assert.equal(await page.evaluate(() => window.__fakeKeychain), '');
      assert.equal(await page.evaluate(key => sessionStorage.getItem(key), barrierKey), 'blocked');
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_CANCELLED_ACCESS'));
    });
    await scenario('protocol expiry during native store invalidates its authentication generation', {
      native: { storeDelay: 600000 },
      exchange: (route, call) => call.body.protocolSelections
        ? fulfill(route, { ...session('EXPIRED'), refreshToken: 'FAKE_EXPIRED_REFRESH' })
        : protocolRequired(route, [protocolItem(71)]),
    }, async ({ page, origin, calls }) => {
      await page.clock.install();
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`); await protocolPanel(page);
      await chooseProtocol(page, 71, 'OpenAI Responses.*\\/v1/responses');
      await confirmProtocols(page).click();
      await page.waitForFunction(() => window.__fakeShellCalls.includes('store_session'));
      await page.clock.fastForward(300001);
      await page.clock.fastForward(300001);
      await page.waitForFunction(() => window.__fakeShellCalls.filter(command => command === 'clear_session').length >= 2, null, { timeout: 5000 });
      await failure(page);
      assert.equal(await page.evaluate(() => window.__fakeKeychain), '');
      assert.equal(await page.evaluate(key => sessionStorage.getItem(key), barrierKey), 'blocked');
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_EXPIRED_ACCESS'));
    });
    await scenario('same-document new fragment is cleaned and starts one new attempt', { exchange: (route, call) => call.body.userID === 2 ? fulfill(route, session()) : route.fulfill({ status: 401, contentType: 'application/json', body: '{}' }) }, async ({ page, origin, exchanges, checkClean }) => {
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`); await failure(page);
      await page.goto(`${origin}/#user_id=2&token=FAKE_SECOND_PANEL_JWT`);
      await page.waitForURL('**/chat'); await checkClean(); assert.equal(exchanges().length, 2);
      assert.deepEqual(exchanges().map(call => call.body.userID), [1, 2]);
    });
    await scenario('client history navigation captures query credentials before old auth can restore', { refresh: route => route.fulfill({ status: 401, contentType: 'application/json', body: '{}' }), exchange: route => fulfill(route, session()) }, async ({ page, origin, exchanges, checkClean }) => {
      await page.goto(`${origin}/login`); await page.locator('input[name=username]').waitFor();
      await page.evaluate(() => history.pushState(null, '', '/?user_id=2&token=FAKE_PANEL_JWT'));
      await page.waitForFunction(() => !location.search.includes('token'));
      await delay(500); await checkClean(); assert.equal(exchanges().length, 1);
    });
    await scenario('slow superseded exchange drains before new exchange and cannot commit', { exchange: async (route, call) => { if (call.body.userID === 1) await delay(350); await fulfill(route, session(call.body.userID === 1 ? 'STALE' : 'NEW')); } }, async ({ page, origin, calls, exchanges, checkClean }) => {
      const requested = page.waitForRequest('**/api/v1/auth/sub2api/exchange');
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`); await requested;
      await page.goto(`${origin}/#user_id=2&token=FAKE_SECOND_PANEL_JWT`);
      await page.waitForURL('**/chat'); await checkClean();
      assert.equal(exchanges().length, 2);
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_STALE_ACCESS'));
    });
    await scenario('in-flight old refresh cannot write into a new switch', { refresh: async route => { await delay(500); await fulfill(route, session('OLD')); }, exchange: route => fulfill(route, session()) }, async ({ page, origin, calls, exchanges, checkClean }) => {
      const requested = page.waitForRequest('**/api/v1/auth/refresh');
      await page.goto(`${origin}/login`); await requested;
      await page.evaluate(() => history.pushState(null, '', '/#user_id=2&token=FAKE_PANEL_JWT'));
      await page.waitForURL('**/chat'); await checkClean(); assert.equal(exchanges().length, 1);
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_OLD_ACCESS'));
    });
    await scenario('external profile and partial import summary stay visible without blocking chat', (() => {
      const externalUser = { ...user, username: 'sub2api_generated_internal', displayName: 'sub2api user', externalProfile: { provider: 'sub2api', displayName: 'External display', username: 'external-nickname', email: '{{TEST_EMAIL_2}}' } };
      const rawReason = 'RAW_API_KEY_SECRET must not render';
      const sub2apiImport = {
        total: 4, eligible: 4, imported: 1, reused: 1, skipped: 1, failed: 1,
        items: [
          { keyID: 1, name: 'Key A', groupName: 'OpenAI', providerID: 'provider001', status: 'imported' },
          { keyID: 2, name: 'Key B', groupName: 'Anthropic', providerID: 'provider002', status: 'reused' },
          { keyID: 3, name: 'Key C', groupName: 'OpenAI', status: 'skipped', reason: 'quota_exhausted' },
          { keyID: 4, name: 'Key D', groupName: 'Gemini', status: 'failed', reason: rawReason },
        ],
      };
      return { viewer: externalUser, exchange: route => fulfill(route, { ...session(), user: externalUser, sub2apiImport }) };
    })(), async ({ page, origin }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`);
      await page.waitForURL('**/chat');
      await page.locator('[data-sub2api-import-summary]').waitFor();
      assert.equal(new URL(page.url()).pathname, '/chat');
      await page.getByText('2 services are ready; some Keys were not imported', { exact: true }).waitFor();
      const summary = page.locator('[data-sub2api-import-summary]');
      await summary.locator('details summary').click();
      assert((await summary.innerText()).includes('Key is over quota'));
      assert(!(await page.locator('body').innerText()).includes('RAW_API_KEY_SECRET'));
      const trigger = page.locator('#sidebar-user-menu-trigger');
      assert((await trigger.innerText()).includes('external-nickname'));
      assert(!(await trigger.innerText()).includes('sub2api_generated_internal'));
      await trigger.click();
      const menu = page.getByRole('menu');
      await menu.waitFor();
      assert((await menu.innerText()).includes('external-nickname'));
      assert((await menu.innerText()).includes('{{TEST_EMAIL_2}}'));
      await page.goto(`${origin}/settings/account`);
      await page.getByText('sub2api source email', { exact: true }).waitFor({ timeout: 5000 });
      assert((await page.locator('body').innerText()).includes('not a security email'));
      assert.equal(await page.getByText('Email ({{TEST_EMAIL_2}})', { exact: true }).count(), 0);
    });
    await scenario('two-factor import warnings remain hidden until verification succeeds', {
      next: '/',
      exchange: route => fulfill(route, { ...challenge, sub2apiImport: {
        total: 2, eligible: 2, imported: 0, reused: 1, skipped: 1, failed: 0,
        items: [
          { keyID: 1, name: 'Existing Key', groupName: 'OpenAI', status: 'reused' },
          { keyID: 2, name: 'New Key', groupName: 'OpenAI', status: 'skipped', reason: 'two_factor_required' },
        ],
      } }),
    }, async ({ page, origin }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`);
      await page.waitForURL('**/login');
      assert.equal(await page.locator('[data-sub2api-import-summary]').count(), 0);
      await page.locator('input[name=otp]').fill('123456');
      await page.locator('button[type=submit]').click();
      await page.waitForURL('**/chat');
      const summary = page.locator('[data-sub2api-import-summary]');
      await summary.waitFor();
      await summary.locator('details summary').click();
      assert((await summary.innerText()).includes('New Key'));
      assert((await summary.innerText()).includes('will not automatically import'));
    });
    await scenario('all-failed import shows precise quota details and no raw upstream error', {
      exchange: route => route.fulfill({ status: 403, contentType: 'application/json', body: JSON.stringify({
        errorCode: 'auth.sub2api_import_failed', errorMsg: 'FAKE_RAW_SECRET', details: { sub2apiImport: {
          total: 1, eligible: 1, imported: 0, reused: 0, skipped: 0, failed: 1,
          items: [{ keyID: 1, name: 'Limited Key', groupName: 'OpenAI', status: 'failed', reason: 'limit_reached' }],
        } },
      }) }),
    }, async ({ page, origin }) => {
      await page.goto(`${origin}/#user_id=2&token=FAKE_PANEL_JWT`);
      await failure(page);
      const summary = page.locator('[data-sub2api-import-summary]');
      await summary.locator('details summary').click();
      assert.match(await summary.innerText(), /personal-provider limit|个人模型服务数量上限/);
      assert(!(await page.locator('body').innerText()).includes('FAKE_RAW_SECRET'));
    });
    await scenario('native malformed entry clears keychain before showing failure', { native: {} }, async ({ page, origin, refreshes, exchanges }) => {
      await page.goto(`${origin}/?user_id=1`); await failure(page);
      assert.equal(await page.evaluate(() => window.__fakeKeychain), '');
      assert((await page.evaluate(() => window.__fakeShellCalls)).includes('clear_session'));
      assert.equal(exchanges().length, 0); assert.equal(refreshes().length, 0);
    });
    await scenario('native clear failure remains closed and never exchanges', { native: { clearFails: true } }, async ({ page, origin, exchanges }) => {
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`); await failure(page);
      assert.equal(exchanges().length, 0);
      assert(!(await page.evaluate(() => window.__fakeShellCalls)).includes('refresh_session'));
      assert.equal(await page.evaluate(key => sessionStorage.getItem(key), barrierKey), 'blocked');
    });
    await scenario('superseded native store is cleared before the next attempt commits', {
      native: { storeDelay: 300 },
      exchange: (route, call) => fulfill(route, { ...session(call.body.userID === 1 ? 'STALE' : 'NEW'), refreshToken: call.body.userID === 1 ? 'FAKE_STALE_REFRESH' : 'FAKE_NEW_REFRESH' }),
    }, async ({ page, origin, calls, exchanges }) => {
      await page.goto(`${origin}/#user_id=1&token=FAKE_PANEL_JWT`);
      await page.waitForFunction(() => window.__fakeShellCalls.includes('store_session'));
      await page.goto(`${origin}/#user_id=2&token=FAKE_SECOND_PANEL_JWT`); await page.waitForURL('**/chat');
      assert.equal(exchanges().length, 2);
      assert.equal(await page.evaluate(() => window.__fakeKeychain), 'FAKE_NEW_REFRESH');
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_STALE_ACCESS'));
      const shell = await page.evaluate(() => window.__fakeShellCalls.filter(command => ['clear_session', 'store_session', 'refresh_session'].includes(command)));
      assert.deepEqual(shell, ['clear_session', 'store_session', 'clear_session', 'clear_session', 'store_session']);
    });
    console.log(`${passed} browser scenarios passed`);
  } finally { await browser.close(); }
}
main().catch(error => { console.error(error); process.exitCode = 1; }).finally(() => server.close());
