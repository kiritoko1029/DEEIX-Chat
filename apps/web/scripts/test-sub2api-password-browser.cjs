/* Build apps/web first. Uses only fake HTTP, credentials, captcha, and flows. */
/* biome-ignore-all lint/correctness/noUnusedFunctionParameters: fake route handlers keep one shared signature. */
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = path.resolve(__dirname, '../out');
const server = http.createServer((request, response) => {
  let file = path.join(root, decodeURIComponent(new URL(request.url, 'http://fake').pathname));
  if (!file.startsWith(root + path.sep) && file !== root) { response.writeHead(403); return response.end(); }
  if (fs.existsSync(file) && fs.statSync(file).isDirectory()) file = fs.existsSync(`${file}.html`) ? `${file}.html` : path.join(file, 'index.html');
  if (!fs.existsSync(file) && fs.existsSync(`${file}.html`)) file += '.html';
  if (!fs.existsSync(file)) { response.writeHead(404); return response.end(); }
  response.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.txt': 'text/x-component', '.json': 'application/json', '.svg': 'image/svg+xml' })[path.extname(file)] || 'application/octet-stream');
  fs.createReadStream(file).pipe(response);
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const user = { id: 2, publicID: 'fake-user-2', username: 'fake-user', displayName: 'Fake user', role: 'user', status: 'active', timezone: 'UTC', locale: 'en-US', phone: '', email: '', avatarURL: '', profilePreferences: '{}', appearancePreferences: '{}', identityProviders: [], initialSecurityRequired: false, initialUsernameRequired: false, onboardingCompletedAt: '2026-01-01T00:00:00Z' };
const session = (name = 'REMOTE') => ({ accessToken: `FAKE_${name}_ACCESS`, sessionID: `FAKE_${name}_SESSION`, user });
const flowID = `FAKE_FLOW_${'A'.repeat(33)}`;
const expiresAt = () => new Date(Date.now() + 240000).toISOString();
const remote2fa = (id = flowID, expiry = expiresAt()) => ({ stage: 'remote_2fa', flowID: id, expiresAt: expiry, maskedEmail: 'u***@example.invalid' });
const protocols = ['openai_responses', 'anthropic_messages', 'openai_chat_completions'];
const protocolError = (items, id = flowID, expiry = expiresAt()) => ({
  errorCode: 'auth.sub2api_protocol_required', errorMsg: 'FAKE_RAW_SECRET', details: { stage: 'protocol_selection', flowID: id, expiresAt: expiry, protocolSelection: { items, protocols } },
});
const loginOptions = (provider = 'none') => ({ enabled: true, passwordLoginAvailable: true, browserOnly: true, unavailableReason: null, upstreamVersion: '0.2.21', remoteTotpEnabled: true, captcha: { provider, siteKey: provider === 'turnstile' ? 'FAKE_PUBLIC_SITE_KEY' : '' }, fallbackURL: 'https://sub.example.invalid/login' });
const summary = { total: 2, eligible: 1, imported: 1, reused: 0, skipped: 1, failed: 0, items: [{ keyID: 71, name: 'Key A', groupName: 'Custom', status: 'imported' }, { keyID: 72, name: 'Skipped Key', groupName: 'Custom', status: 'skipped', reason: 'user_skipped' }] };

async function main() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
  let passed = 0;
  async function scenario(name, options = {}, run) {
    if (process.env.SUB2API_TEST_FILTER && !name.includes(process.env.SUB2API_TEST_FILTER)) return;
    const context = await browser.newContext();
    try {
      await context.addInitScript(({ captchaMode }) => {
        window.open = url => { window.__openedExternal = String(url); return null; };
        window.__captchaResets = 0;
        if (captchaMode === 'none' || captchaMode === 'script-error' || captchaMode === 'timeout') return;
        let token = 0;
        let callbacks;
        window.turnstile = {
          render(_element, options) {
            callbacks = options;
            if (captchaMode === 'render-error') queueMicrotask(() => options['error-callback']());
            else queueMicrotask(() => options.callback(`FAKE_CAPTCHA_${++token}`));
            return 'fake-widget';
          },
          reset() { window.__captchaResets++; queueMicrotask(() => callbacks.callback(`FAKE_CAPTCHA_${++token}`)); },
          remove() {},
        };
      }, { captchaMode: options.captcha || 'none' });
      if (options.localChallenge) await context.addInitScript(() => {
        sessionStorage.setItem('deeix-chat:2fa:challenge', JSON.stringify({ token: 'FAKE_STORED_LOCAL_CHALLENGE', methods: ['two_factor'], expiresAt: Date.now() + 240000 }));
      });
      const page = await context.newPage();
      await page.route('https://challenges.cloudflare.com/turnstile/v0/api.js*', async route => {
        if (options.captcha === 'script-error') return route.abort('failed');
        if (options.captcha === 'timeout') await delay(16000);
        return route.fulfill({ status: 200, contentType: 'text/javascript', body: '// fake Turnstile script' });
      });
      const calls = [];
      const fulfill = (route, data) => {
        const login = data?.stage === 'authenticated' ? data.login : data;
        const headers = login?.accessToken ? { 'Set-Cookie': 'refresh_token=FAKE_NEW_COOKIE; Path=/api/v1/auth; HttpOnly; SameSite=Lax' } : {};
        return route.fulfill({ status: 200, contentType: 'application/json', headers, body: JSON.stringify({ data }) });
      };
      const reject = (route, status, errorCode, details) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify({ errorCode, errorMsg: 'FAKE_RAW_UPSTREAM_SECRET', details }) });
      await page.route('**/api/v1/**', async route => {
        const request = route.request();
        const endpoint = new URL(request.url()).pathname;
        let body = null;
        try { body = request.postDataJSON(); } catch {}
        const call = { endpoint, body, authorization: request.headers().authorization || '' };
        calls.push(call);
        if (endpoint.endsWith('/sub2api/login-options')) {
          if (options.optionsFail) return reject(route, 503, 'auth.sub2api_password_unavailable');
          return fulfill(route, loginOptions(options.captcha && options.captcha !== 'script-error' ? 'turnstile' : options.captcha === 'script-error' ? 'turnstile' : 'none'));
        }
        if (endpoint.endsWith('/sub2api/login/2fa')) {
          if (options.twoFactor) return options.twoFactor(route, call, calls, reject, fulfill);
          return fulfill(route, { stage: 'authenticated', login: session() });
        }
        if (endpoint.endsWith('/sub2api/login/continue')) {
          if (options.continue) return options.continue(route, call, calls, reject, fulfill);
          return fulfill(route, { stage: 'authenticated', login: session() });
        }
        if (endpoint.endsWith('/sub2api/login/cancel')) return fulfill(route, { cancelled: true });
        if (endpoint.endsWith('/sub2api/login')) {
          if (options.start) return options.start(route, call, calls, reject, fulfill);
          return fulfill(route, { stage: 'authenticated', login: session() });
        }
        if (endpoint.endsWith('/sub2api/exchange')) return fulfill(route, session('SSO'));
        if (endpoint.endsWith('/auth/refresh')) {
          if (options.refreshAfterSSO && calls.some(item => item.endpoint.endsWith('/sub2api/exchange'))) return fulfill(route, session('SSO'));
          return reject(route, 401, 'auth.unauthorized');
        }
        if (endpoint.endsWith('/auth/2fa/verify')) return fulfill(route, session('LOCAL'));
        if (endpoint.endsWith('/auth/login')) return fulfill(route, session('LOCAL'));
        if (endpoint.endsWith('/settings/login-page')) return fulfill(route, { defaultNextPath: '/chat' });
        if (endpoint.endsWith('/auth/login-options')) return fulfill(route, { usernameEnabled: true, emailEnabled: true, emailVerificationEnabled: false, providers: [], providerAuthBridge: { enabled: false } });
        if (endpoint.endsWith('/auth/sessions') || endpoint.endsWith('/me/identities')) return fulfill(route, { results: [], total: 0 });
        if (endpoint.endsWith('/me/2fa')) return fulfill(route, { available: true, totpEnabled: false, required: false, recoveryCount: 0, enabledAt: null });
        if (endpoint === '/api/v1/me') return fulfill(route, { user });
        if (endpoint.includes('conversation-projects') || endpoint.includes('announcements') || endpoint.endsWith('/models')) return fulfill(route, []);
        if (endpoint.includes('conversations')) return fulfill(route, { results: [], total: 0 });
        return fulfill(route, {});
      });
      const subLogins = () => calls.filter(call => call.endpoint.endsWith('/sub2api/login'));
      const localLogins = () => calls.filter(call => call.endpoint.endsWith('/auth/login'));
      const checkClean = async () => {
        const serialized = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage }, history: history.state, url: location.href }));
        for (const secret of ['FAKE_REMOTE_PASSWORD', 'FAKE_LOCAL_PASSWORD', 'FAKE_CAPTCHA_', 'FAKE_FLOW_', 'FAKE_RAW_UPSTREAM_SECRET']) assert(!serialized.includes(secret), `sensitive value persisted: ${secret}`);
      };
      await run({ page, context, origin, calls, subLogins, localLogins, checkClean });
      passed++;
      console.log(`PASS ${name}`);
    } finally { await context.close(); }
  }
  const waitSubForm = page => page.locator('[data-sub2api-password-form]').waitFor();
  const fillSub = async (page, password = 'FAKE_REMOTE_PASSWORD') => {
    await waitSubForm(page);
    await page.locator('#sub2api-email').fill('  user@example.invalid  ');
    await page.locator('#sub2api-password').fill(password);
  };
  const submitSub = page => page.locator('[data-sub2api-password-form] button[type=submit]').click();
  const choose = async (page, keyID, expression) => {
    await page.getByRole('combobox', { name: new RegExp(`#${keyID}`) }).click();
    await page.getByRole('option', { name: new RegExp(expression) }).click();
  };
  const localLogin = async page => {
    await page.locator('input[name=username]').fill('fake-local-user');
    await page.locator('input[name=password]').fill('FAKE_LOCAL_PASSWORD');
    await page.locator('button[type=submit]').click();
    await page.waitForURL('**/chat');
  };

  try {
    for (const optionsFail of [false, true]) {
      await scenario(optionsFail ? 'failed options preserve local login' : 'disabled options preserve local login', { optionsFail, ...(optionsFail ? {} : { optionsDisabled: true }) }, async ({ page, origin, localLogins, subLogins }) => {
        await page.route('**/api/v1/auth/sub2api/login-options', route => optionsFail ? route.continue() : route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: { ...loginOptions(), enabled: false, passwordLoginAvailable: false, fallbackURL: '', captcha: { provider: 'none', siteKey: '' } } }) }));
        await page.goto(`${origin}/login`);
        await page.locator('input[name=username]').waitFor();
        assert.equal(await page.locator('[data-sub2api-password-form]').count(), 0);
        await localLogin(page);
        assert.equal(localLogins().length, 1); assert.equal(subLogins().length, 0);
      });
    }
    await scenario('none mode trims only email and authenticates without local fallback', {}, async ({ page, origin, subLogins, localLogins, checkClean }) => {
      await page.goto(`${origin}/login`); await fillSub(page, '  FAKE_REMOTE_PASSWORD  '); await submitSub(page);
      await page.waitForURL('**/chat'); await checkClean();
      assert.equal(subLogins().length, 1); assert.equal(localLogins().length, 0);
      assert.equal(subLogins()[0].body.email, 'user@example.invalid');
      assert.equal(subLogins()[0].body.password, '  FAKE_REMOTE_PASSWORD  ');
      assert.deepEqual(subLogins()[0].body.captcha, { provider: 'none' });
    });
    for (const captcha of ['render-error', 'script-error']) {
      await scenario(`${captcha} blocks password submission and offers trusted fallback`, { captcha }, async ({ page, origin, subLogins }) => {
        await page.goto(`${origin}/login`); await fillSub(page);
        await page.locator('p[role=alert]').waitFor();
        assert.equal(await page.locator('[data-sub2api-password-form] button[type=submit]').isDisabled(), true);
        await page.getByRole('button', { name: /Manage or sign in at sub2api|前往 sub2api/ }).click();
        assert.equal(await page.evaluate(() => window.__openedExternal), 'https://sub.example.invalid/login');
        assert.equal(subLogins().length, 0);
      });
    }
    await scenario('captcha initialization timeout blocks password submission', { captcha: 'timeout' }, async ({ page, origin, subLogins }) => {
      await page.goto(`${origin}/login`); await fillSub(page);
      await page.locator('p[role=alert]').waitFor({ timeout: 25000 });
      assert.equal(await page.locator('[data-sub2api-password-form] button[type=submit]').isDisabled(), true);
      assert.equal(subLogins().length, 0);
    });
    await scenario('captcha failure clears password and resets single-use proof', {
      captcha: 'turnstile',
      start: (route, call, calls, reject, fulfill) => {
        const count = calls.filter(item => item.endpoint.endsWith('/sub2api/login')).length;
        if (count === 1) return reject(route, 400, 'auth.sub2api_captcha_failed');
        return fulfill(route, { stage: 'authenticated', login: session() });
      },
    }, async ({ page, origin, subLogins, checkClean }) => {
      await page.goto(`${origin}/login`); await fillSub(page);
      await page.locator('[data-sub2api-password-form] button[type=submit]:not([disabled])').waitFor(); await submitSub(page);
      await page.getByRole('alert').waitFor();
      assert.equal(await page.locator('#sub2api-password').inputValue(), '');
      assert.equal(await page.evaluate(() => window.__captchaResets), 1);
      await page.locator('#sub2api-password').fill('FAKE_REMOTE_PASSWORD');
      await page.locator('[data-sub2api-password-form] button[type=submit]:not([disabled])').waitFor();
      await submitSub(page); await page.waitForURL('**/chat');
      assert.equal(subLogins().length, 2);
      assert.notEqual(subLogins()[0].body.captcha.token, subLogins()[1].body.captcha.token);
      await checkClean();
    });
    for (const stage of ['remote_2fa', 'protocol_selection']) {
      await scenario(`network latency accepts the first five-minute ${stage} deadline`, {
        start: async (route, call, calls, reject, fulfill) => {
          await delay(150);
          call.expiry = new Date(Date.now() + 300000).toISOString();
          if (stage === 'remote_2fa') return fulfill(route, remote2fa(flowID, call.expiry));
          return reject(route, 409, 'auth.sub2api_protocol_required', protocolError([{ keyID: 71, name: 'Delayed Key', groupName: 'Custom', platform: 'unknown' }], flowID, call.expiry).details);
        },
      }, async ({ page, origin, subLogins, checkClean }) => {
        await page.goto(`${origin}/login`); await fillSub(page); await submitSub(page);
        if (stage === 'remote_2fa') {
          await page.locator('#sub2api-remote-otp').fill('123456');
          await page.getByRole('button', { name: /Verify sub2api code|验证 sub2api/ }).click();
        } else {
          await choose(page, 71, 'OpenAI Responses');
          await page.locator('[data-sub2api-protocol-confirm]').click();
        }
        await page.waitForURL('**/chat'); await checkClean();
        assert.equal(subLogins().length, 1);
      });
    }
    await scenario('new SSO entry retires the old password flow timer', {
      refreshAfterSSO: true,
      start: (route, call, calls, reject, fulfill) => fulfill(route, remote2fa()),
    }, async ({ page, context, origin, calls, checkClean }) => {
      await page.clock.install();
      await page.goto(`${origin}/login`); await fillSub(page); await submitSub(page);
      await page.locator('#sub2api-remote-otp').waitFor();
      await page.evaluate(() => history.pushState(null, '', '/?user_id=2&token=FAKE_PANEL_JWT'));
      await page.waitForURL('**/chat'); await checkClean();
      const cancelled = calls.filter(call => call.endpoint.endsWith('/sub2api/login/cancel')).length;
      await page.clock.fastForward(300001);
      await page.waitForFunction(() => sessionStorage.getItem('deeix-chat:account-switch') === 'active');
      assert.equal(calls.filter(call => call.endpoint.endsWith('/sub2api/login/cancel')).length, cancelled);
      assert((await context.cookies()).some(cookie => cookie.name === 'refresh_token' && cookie.value === 'FAKE_NEW_COOKIE'));
    });
    await scenario('remote TOTP retry preserves flow and never invokes Chat 2FA', {
      start: (route, call, calls, reject, fulfill) => { call.expiry = expiresAt(); return fulfill(route, remote2fa(flowID, call.expiry)); },
      twoFactor: (route, call, calls, reject, fulfill) => {
        const original = calls.find(item => item.endpoint.endsWith('/sub2api/login'));
        if (call.body.code === '111111') return reject(route, 400, 'auth.sub2api_remote_two_factor_invalid', remote2fa(flowID, original.expiry));
        return fulfill(route, { stage: 'authenticated', login: session() });
      },
    }, async ({ page, origin, calls, checkClean }) => {
      await page.goto(`${origin}/login`); await fillSub(page); await submitSub(page);
      await page.locator('#sub2api-remote-otp').waitFor(); await checkClean();
      await page.locator('#sub2api-remote-otp').fill('111111');
      await page.getByRole('button', { name: /Verify sub2api code|验证 sub2api/ }).click();
      await page.getByRole('alert').waitFor();
      assert.equal(await page.locator('#sub2api-remote-otp').inputValue(), '');
      await page.locator('#sub2api-remote-otp').fill('222222');
      await page.getByRole('button', { name: /Verify sub2api code|验证 sub2api/ }).click(); await page.waitForURL('**/chat');
      const verifications = calls.filter(call => call.endpoint.endsWith('/sub2api/login/2fa'));
      assert.equal(verifications.length, 2); assert(verifications.every(call => call.body.flowID === flowID));
      assert.equal(calls.filter(call => call.endpoint.endsWith('/auth/2fa/verify')).length, 0);
    });
    await scenario('remote 2FA then Chat local 2FA and pending summary handoff', {
      start: (route, call, calls, reject, fulfill) => fulfill(route, remote2fa()),
      twoFactor: (route, call, calls, reject, fulfill) => fulfill(route, { stage: 'local_2fa', login: { user, twoFactorRequired: true, twoFactorChallengeToken: 'FAKE_LOCAL_CHALLENGE', verificationMethods: ['two_factor'], sub2apiImport: summary } }),
    }, async ({ page, origin, calls, checkClean }) => {
      await page.goto(`${origin}/login`); await fillSub(page); await submitSub(page);
      await page.locator('#sub2api-remote-otp').fill('123456');
      await page.getByRole('button', { name: /Verify sub2api code|验证 sub2api/ }).click();
      await page.locator('#otp').waitFor(); await checkClean();
      assert.equal(await page.locator('#sub2api-remote-otp').count(), 0);
      await page.locator('#otp').fill('654321'); await page.locator('button[type=submit]').click(); await page.waitForURL('**/chat');
      const local = calls.find(call => call.endpoint.endsWith('/auth/2fa/verify'));
      assert.equal(local.body.challengeToken, 'FAKE_LOCAL_CHALLENGE'); assert.equal(local.body.code, '654321');
      await page.locator('[data-sub2api-import-summary]').waitFor();
    });
    await scenario('repeated protocol 409 accumulates decisions and displays skip summary', {
      start: (route, call, calls, reject) => { call.expiry = expiresAt(); return reject(route, 409, 'auth.sub2api_protocol_required', protocolError([{ keyID: 71, name: 'Key A', groupName: 'Custom', platform: 'DeepSeek' }], flowID, call.expiry).details); },
      continue: (route, call, calls, reject, fulfill) => {
        const original = calls.find(item => item.endpoint.endsWith('/sub2api/login'));
        if (call.body.skipKeyIDs.length === 0) return reject(route, 409, 'auth.sub2api_protocol_required', protocolError([{ keyID: 72, name: 'Skipped Key', groupName: 'Custom', platform: 'Other' }], flowID, original.expiry).details);
        return fulfill(route, { stage: 'authenticated', login: { ...session(), sub2apiImport: summary } });
      },
    }, async ({ page, origin, calls, checkClean }) => {
      await page.goto(`${origin}/login`); await fillSub(page); await submitSub(page);
      await page.getByRole('combobox', { name: /#71/ }).waitFor(); await checkClean();
      await choose(page, 71, 'OpenAI Responses'); await page.locator('[data-sub2api-protocol-confirm]').click();
      await page.getByRole('combobox', { name: /#72/ }).waitFor();
      await choose(page, 72, 'Skip for this sign-in|本次跳过'); await page.locator('[data-sub2api-protocol-confirm]').click(); await page.waitForURL('**/chat');
      const continuations = calls.filter(call => call.endpoint.endsWith('/sub2api/login/continue'));
      assert.equal(continuations.length, 2);
      assert.deepEqual(continuations[1].body.protocolSelections, [{ keyID: 71, protocol: 'openai_responses' }]);
      assert.deepEqual(continuations[1].body.skipKeyIDs, [72]);
      assert(!JSON.stringify(continuations).includes('FAKE_REMOTE_PASSWORD'));
      await page.locator('[data-sub2api-import-summary]').waitFor(); await checkClean();
    });
    await scenario('double password and remote code submissions send once each', {
      start: async (route, call, calls, reject, fulfill) => { await delay(250); return fulfill(route, remote2fa()); },
      twoFactor: async (route, call, calls, reject, fulfill) => { await delay(250); return fulfill(route, { stage: 'authenticated', login: session() }); },
    }, async ({ page, origin, calls }) => {
      await page.goto(`${origin}/login`); await fillSub(page);
      await page.locator('[data-sub2api-password-form]').evaluate(form => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
      await page.locator('#sub2api-remote-otp').waitFor();
      assert.equal(calls.filter(call => call.endpoint.endsWith('/sub2api/login')).length, 1);
      await page.locator('#sub2api-remote-otp').fill('123456');
      const button = page.getByRole('button', { name: /Verify sub2api code|验证 sub2api/ });
      await button.evaluate(element => { element.click(); element.click(); });
      await page.waitForURL('**/chat');
      assert.equal(calls.filter(call => call.endpoint.endsWith('/sub2api/login/2fa')).length, 1);
    });
    await scenario('cancelling remote flow does not restore an old session', {
      start: (route, call, calls, reject, fulfill) => fulfill(route, remote2fa()),
    }, async ({ page, origin, calls, localLogins, checkClean }) => {
      await page.goto(`${origin}/login`); await fillSub(page); await submitSub(page);
      await page.locator('#sub2api-remote-otp').waitFor();
      await page.getByRole('button', { name: /Cancel sub2api sign-in|取消 sub2api/ }).click(); await waitSubForm(page); await checkClean();
      assert(calls.some(call => call.endpoint.endsWith('/sub2api/login/cancel') && call.body.flowID === flowID));
      await page.locator('#sub2api-password').fill('FAKE_REMOTE_PASSWORD');
      await page.getByRole('button', { name: /Chat local account|Chat 本地账号/ }).click();
      assert.equal(await page.locator('input[name=password]').inputValue(), '');
      await page.getByRole('button', { name: /Use sub2api email|使用 sub2api 邮箱/ }).click(); await waitSubForm(page);
      assert.equal(await page.locator('#sub2api-password').inputValue(), '');
      await page.getByRole('button', { name: /Chat local account|Chat 本地账号/ }).click(); await localLogin(page);
      assert.equal(localLogins().length, 1);
      assert(!calls.some(call => call.authorization === 'Bearer FAKE_REMOTE_ACCESS'));
    });
    await scenario('expired remote flow is terminal and no credential is automatically retried', {
      start: (route, call, calls, reject, fulfill) => fulfill(route, remote2fa()),
      twoFactor: (route, call, calls, reject) => reject(route, 410, 'auth.sub2api_login_flow_expired'),
    }, async ({ page, origin, subLogins, calls, checkClean }) => {
      await page.goto(`${origin}/login`); await fillSub(page); await submitSub(page);
      await page.locator('#sub2api-remote-otp').fill('123456');
      await page.getByRole('button', { name: /Verify sub2api code|验证 sub2api/ }).click();
      await page.getByRole('alert').waitFor(); await waitSubForm(page); await checkClean();
      assert.equal(subLogins().length, 1);
      assert(calls.some(call => call.endpoint.endsWith('/sub2api/login/cancel')));
      assert.equal(await page.locator('#sub2api-password').inputValue(), '');
    });
    await scenario('mode switch drains and cancels an old delayed response before local login', {
      start: async (route, call, calls, reject, fulfill) => { await delay(350); return fulfill(route, remote2fa()); },
    }, async ({ page, origin, calls, checkClean }) => {
      await page.goto(`${origin}/login`); await fillSub(page);
      const requested = page.waitForRequest('**/api/v1/auth/sub2api/login'); await submitSub(page); await requested;
      await page.getByRole('button', { name: /Chat local account|Chat 本地账号/ }).click();
      await localLogin(page); await checkClean();
      assert.equal(await page.locator('#sub2api-remote-otp').count(), 0);
      const order = calls.filter(call => ['/api/v1/auth/sub2api/login', '/api/v1/auth/sub2api/login/cancel', '/api/v1/auth/login'].includes(call.endpoint)).map(call => call.endpoint);
      assert.deepEqual(order, ['/api/v1/auth/sub2api/login', '/api/v1/auth/sub2api/login/cancel', '/api/v1/auth/login']);
    });
    await scenario('stored local challenge wins over available sub2api default after refresh', {
      localChallenge: true,
    }, async ({ page, origin, subLogins, calls }) => {
      await page.goto(`${origin}/login`); await page.locator('#otp').waitFor();
      assert.equal(await page.locator('[data-sub2api-password-form]').count(), 0);
      await page.reload(); await page.locator('#otp').waitFor();
      assert.equal(await page.locator('[data-sub2api-password-form]').count(), 0);
      await page.locator('#otp').fill('123456'); await page.locator('button[type=submit]').click(); await page.waitForURL('**/chat');
      assert.equal(subLogins().length, 0);
      assert.equal(calls.find(call => call.endpoint.endsWith('/auth/2fa/verify')).body.challengeToken, 'FAKE_STORED_LOCAL_CHALLENGE');
    });
    console.log(`${passed} sub2api password browser scenarios passed`);
  } finally { await browser.close(); }
}
main().catch(error => { console.error(error); process.exitCode = 1; }).finally(() => server.close());
