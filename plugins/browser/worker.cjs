// Private newline-delimited protocol. stdout is reserved for responses.
const readline = require('node:readline');
const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');

function publicURL(raw) {
  const u = new URL(raw);
  if (!['http:', 'https:'].includes(u.protocol) || u.username || u.password)
    throw new Error('unsupported URL');
  return u.href;
}

async function inboxFile(root, file) {
  if (path.isAbsolute(file)) throw new Error('file outside inbox');
  const inbox = await fs.realpath(path.join(root, 'inbox'));
  const target = await fs.realpath(path.resolve(inbox, file));
  const relative = path.relative(inbox, target);
  if (!relative || relative.startsWith('..') || path.isAbsolute(relative))
    throw new Error('file outside inbox');
  const stat = await fs.stat(target);
  if (!stat.isFile() || stat.size > 10 * 1024 * 1024) throw new Error('invalid upload file');
  // Reading into a buffer prevents a symlink replacement after validation from
  // making Playwright open a different file by path.
  const handle = await fs.open(target, 'r');
  try {
    const actual = await handle.stat();
    if (actual.ino !== stat.ino || actual.dev !== stat.dev || actual.size > 10 * 1024 * 1024)
      throw new Error('upload file changed');
    const buffer = Buffer.alloc(10 * 1024 * 1024 + 1);
    let size = 0;
    while (size < buffer.length) {
      const { bytesRead } = await handle.read(buffer, size, buffer.length - size, null);
      if (!bytesRead) break;
      size += bytesRead;
    }
    if (size > 10 * 1024 * 1024) throw new Error('upload file grew beyond limit');
    return { name: path.basename(target), mimeType: 'application/octet-stream', buffer: buffer.subarray(0, size) };
  } finally { await handle.close(); }
}

async function snapshot(page, limit) {
  const info = await page.evaluate((max) => {
    const elements = [...document.querySelectorAll('a,button,input,textarea,select,[role="button"],[contenteditable="true"]')]
      .filter(el => el.getClientRects().length && getComputedStyle(el).visibility !== 'hidden')
      .slice(0, 80).map(el => ({
        tag: el.tagName.toLowerCase(), type: el.getAttribute('type') || '',
        label: (el.getAttribute('aria-label') || el.innerText || el.getAttribute('placeholder') || '').slice(0, 160),
        selector: el.id ? '#' + CSS.escape(el.id) : el.getAttribute('name') ? el.tagName.toLowerCase() + '[name=' + JSON.stringify(el.getAttribute('name')) + ']' : '',
      }));
    return { title: document.title.slice(0, 300), text: (document.body?.innerText || '').slice(0, max), elements };
  }, limit);
  return JSON.stringify({ url: page.url(), ...info });
}

function makeSession(config, chromium) {
  let context;
  let next = 1;
  let active = 0;
  const tabs = new Map();
  const register = page => {
    if (tabs.size >= 8) { void page.close().catch(() => {}); return; }
    const id = next++;
    tabs.set(id, page);
    active = id;
    page.on('close', () => { tabs.delete(id); if (active === id) active = [...tabs.keys()].pop() || 0; });
    page.on('dialog', dialog => { void dialog.dismiss().catch(() => {}); });
  };
  async function launch() {
    if (context) return;
    for (const name of ['profile', 'inbox', 'screenshots'])
      await fs.mkdir(path.join(config.dir, name), { recursive: true, mode: 0o700 });
    context = await chromium.launchPersistentContext(path.join(config.dir, 'profile'), {
      headless: config.headless, chromiumSandbox: config.sandbox,
      proxy: { server: config.proxy, username: config.username, password: config.password, bypass: '<-loopback>' },
      serviceWorkers: 'block', acceptDownloads: false, viewport: { width: 1280, height: 800 },
      timeout: config.timeout,
      args: ['--disable-dev-shm-usage', '--disable-quic', '--disable-background-networking',
        '--force-webrtc-ip-handling-policy=disable_non_proxied_udp', '--disable-extensions',
        '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'],
    });
    context.setDefaultTimeout(config.timeout);
    context.setDefaultNavigationTimeout(config.timeout);
    // Every browser HTTP connection goes through the authenticated Go proxy,
    // including redirects, images and scripts. No arbitrary evaluate tool exists.
    await context.route('**/*', async route => {
      try { publicURL(route.request().url()); await route.continue(); }
      catch { await route.abort('blockedbyclient').catch(() => {}); }
    });
    await context.routeWebSocket('**/*', socket => socket.close());
    context.on('page', register);
    for (const page of context.pages()) register(page);
  }
  async function run(action, args) {
    await launch();
    if (action === 'list_tabs') {
      const result = [];
      for (const [id, p] of tabs) result.push({ tab: id, url: p.url(), title: (await p.title()).slice(0, 300), active: id === active });
      return JSON.stringify(result);
    }
    let id = args.tab || active;
    if (!id && !args.tab && action === 'navigate_page') { await context.newPage(); id = active; }
    const page = tabs.get(id);
    if (!page) throw new Error('tab unavailable');
    active = id;
    switch (action) {
      case 'navigate_page': {
        const response = await page.goto(publicURL(args.url), { waitUntil: 'domcontentloaded' });
        if (response && response.status() >= 400) throw new Error('navigation refused or unavailable');
        break;
      }
      case 'read_page': break;
      case 'click_element': await page.locator(args.selector).click(); break;
      case 'fill_field': await page.locator(args.selector).fill(args.text || ''); break;
      case 'press_key': await page.keyboard.press(args.key); break;
      case 'scroll_page': await page.mouse.wheel(0, args.pixels); break;
      case 'upload_file': await page.locator(args.selector).setInputFiles(await inboxFile(config.dir, args.file)); break;
      case 'capture_screenshot': {
        const file = path.join(config.dir, 'screenshots', crypto.randomUUID() + '.png');
        await page.screenshot({ path: file, fullPage: false, timeout: config.timeout });
        await fs.chmod(file, 0o600);
        return JSON.stringify({ tab: id, screenshot: file });
      }
      case 'close_tab': await page.close(); return 'Tab closed.';
      default: throw new Error('unknown action');
    }
    return JSON.stringify({ tab: id, page: JSON.parse(await snapshot(page, config.maxChars)) });
  }
  return { run, close: async () => { if (context) { await context.close(); context = undefined; } } };
}

async function main() {
  const lines = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
  let session;
  let queue = Promise.resolve();
  let configured = false;
  const shutdown = async () => { if (session) await session.close().catch(() => {}); process.exit(0); };
  process.on('SIGTERM', shutdown);
  process.on('SIGINT', shutdown);
  lines.on('close', shutdown);
  lines.on('line', line => {
    queue = queue.then(async () => {
      if (!configured) {
        const config = JSON.parse(line);
        const { chromium } = require(config.module);
        session = makeSession(config, chromium);
        configured = true;
        return;
      }
      try {
        const { action, args } = JSON.parse(line);
        const content = await session.run(action, args);
        process.stdout.write(JSON.stringify({ content }) + '\n');
      } catch {
        // Raw Playwright errors include page content and typed secrets.
        process.stdout.write(JSON.stringify({ error: 'Browser action could not complete. Check the tab, selector, network policy and runtime setup.' }) + '\n');
      }
    }).catch(() => { process.stdout.write(JSON.stringify({ error: 'Browser runtime unavailable. Check Playwright installation and Chromium sandbox support.' }) + '\n'); });
  });
}

module.exports = { publicURL, inboxFile, makeSession };
if (require.main === module || process.argv[1] === undefined) void main();
