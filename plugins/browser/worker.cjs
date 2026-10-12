// Private newline-delimited protocol. stdout is reserved for responses.
const readline = require('node:readline');
const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');

function launchError(error) {
  const message = typeof error?.message === 'string' ? error.message : '';
  if (/No usable sandbox|sys_chroot|Failed to move to new namespace|Running as root without --no-sandbox|namespace.*Operation not permitted|zygote_host_impl_linux.*Operation not permitted/i.test(message))
    return 'Chromium sandbox is unavailable. Check the node seccomp, AppArmor and user namespace settings.';
  if (/Executable doesn.t exist|executable.*ENOENT/i.test(message))
    return 'Chromium executable is missing. Install the browser matching the pinned Playwright version.';
  if (/error while loading shared libraries|Host system is missing dependencies/i.test(message))
    return 'Chromium system libraries are missing. Use the browser runtime image.';
  if (/Missing X server|cannot open display/i.test(message))
    return 'Chromium has no display. Enable headless mode or start the desktop runtime.';
  if (/ProcessSingleton|SingletonLock|profile.*in use/i.test(message))
    return 'Chromium profile is already in use. Stop other workers using this profile.';
  if (error?.code === 'EACCES' || /Permission denied/i.test(message))
    return 'Chromium cannot access its files. Check browser data and executable permissions.';
  return 'Chromium could not launch. Run the container launch diagnostic to inspect its runtime.';
}

class LaunchFailure extends Error {
  constructor(error) { super(launchError(error)); }
}

// UserError messages are written in this file and contain no page content, so
// they may be shown to the model. Anything else is reduced to a generic line.
class UserError extends Error {}

function friendly(error) {
  if (error instanceof LaunchFailure || error instanceof UserError) return error.message;
  const message = typeof error?.message === 'string' ? error.message : '';
  const strict = /strict mode violation.*resolved to (\d+) elements/is.exec(message);
  if (strict) return 'That selector matches ' + strict[1] + ' elements. Use a ref from read_page instead.';
  if (/intercepts pointer events/i.test(message))
    return 'Another element is covering the target, such as a popup, cookie banner or sticky bar. Dismiss it or scroll, then try again.';
  const net = /net::(ERR_[A-Z_]+)/.exec(message);
  if (net) return 'The page could not be reached (' + net[1] + ').';
  if (/has been closed/i.test(message)) return 'The tab was closed.';
  if (/Timeout .* exceeded/i.test(message) && /goto|navigat|waitForLoadState|reload|goBack|goForward/i.test(message))
    return 'The page did not finish loading in time.';
  if (/not visible|not enabled|not editable|disabled/i.test(message))
    return 'The element is not visible, enabled or editable right now. Scroll to it or wait for the page, then read it again.';
  if (/Timeout .* exceeded/i.test(message))
    return 'The element was not found or could not be used in time. Read the page again and use a current ref.';
  if (/not a <select>|not an <select>/i.test(message)) return 'That element is not a dropdown.';
  return 'Browser action could not complete. Check the tab, selector, network policy and runtime setup.';
}

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

// pageCollect runs inside a page frame. It must be self-contained: Playwright
// serializes it, so it cannot see anything from this module.
function pageCollect(o) {
  const SELECTOR = 'a[href],button,input:not([type=hidden]),textarea,select,summary,[role=button],[role=link],[role=tab],[role=menuitem],[role=checkbox],[role=radio],[role=switch],[role=option],[role=combobox],[role=searchbox],[role=textbox],[contenteditable=""],[contenteditable="true"],[onclick]';
  const state = window.__kest || (window.__kest = { n: 0, refs: new WeakMap() });
  // A page can write its own attributes; drop any that this code did not assign.
  for (const el of document.querySelectorAll('[data-kest-ref]')) if (!state.refs.has(el)) el.removeAttribute('data-kest-ref');
  const vh = window.innerHeight, vw = window.innerWidth;
  const root = document.scrollingElement || document.documentElement;
  const clean = s => (s || '').replace(/\s+/g, ' ').trim();
  const shown = el => {
    if (!el.getClientRects().length) return false;
    const cs = getComputedStyle(el);
    return cs.visibility !== 'hidden' && cs.display !== 'none';
  };
  const inView = el => {
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < vh && r.right > 0 && r.left < vw;
  };
  const labelOf = el => {
    // A dropdown's or text box's inner text is its options or its content, not its name.
    const named = el.tagName === 'SELECT' || el.tagName === 'TEXTAREA' || el.tagName === 'INPUT';
    return clean(
      el.getAttribute('aria-label') ||
      (el.labels && el.labels[0] && el.labels[0].innerText) ||
      (el.tagName === 'INPUT' && ['button', 'submit', 'reset'].includes(el.type) ? el.value : '') ||
      (named ? '' : el.innerText) || el.getAttribute('placeholder') || el.getAttribute('title') || el.getAttribute('alt') ||
      (el.querySelector && el.querySelector('img[alt]') && el.querySelector('img[alt]').getAttribute('alt')) ||
      el.getAttribute('name') || el.id || '').slice(0, 100);
  };
  state.labelOf = labelOf;

  const elements = [];
  for (const el of document.querySelectorAll(SELECTOR)) {
    if (elements.length >= o.limit) break;
    if (!shown(el)) continue;
    const visible = inView(el);
    if (o.scope === 'viewport' && !visible) continue;
    let ref = state.refs.get(el);
    if (!ref) { ref = o.prefix + 'e' + (state.n++); state.refs.set(el, ref); }
    el.setAttribute('data-kest-ref', ref);
    const tag = el.tagName.toLowerCase();
    const type = (el.getAttribute('type') || '').toLowerCase();
    const rec = { ref, tag, role: el.getAttribute('role') || '', type, label: labelOf(el), visible };
    if (tag === 'a') rec.href = (el.href || '').slice(0, 300);
    if (tag === 'input' || tag === 'textarea' || tag === 'select') {
      if (type !== 'password' && tag !== 'select') rec.value = String(el.value || '').slice(0, 80);
      if (type === 'checkbox' || type === 'radio') rec.checked = Boolean(el.checked);
      if (tag === 'select') rec.options = [...el.options].slice(0, 25).map(op => (clean(op.label || op.text).slice(0, 40) || op.value) + (op.selected ? '*' : ''));
    }
    if (el.hasAttribute('aria-expanded')) rec.expanded = el.getAttribute('aria-expanded') === 'true';
    if (el.hasAttribute('aria-checked')) rec.checked = el.getAttribute('aria-checked') === 'true';
    if (el.disabled) rec.disabled = true;
    elements.push(rec);
  }

  let text = '';
  let total = 0;
  if (o.scope === 'viewport') {
    const parts = [];
    let size = 0, last = null;
    const walker = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT);
    const range = document.createRange();
    for (let n = walker.nextNode(); n && size < o.max; n = walker.nextNode()) {
      const value = n.nodeValue;
      if (!value || !value.trim()) continue;
      const parent = n.parentElement;
      if (!parent || /^(SCRIPT|STYLE|NOSCRIPT|TEMPLATE|TEXTAREA)$/.test(parent.tagName)) continue;
      if (!shown(parent)) continue;
      range.selectNodeContents(n);
      const r = range.getBoundingClientRect();
      if ((!r.width && !r.height) || r.bottom <= 0 || r.top >= vh || r.right <= 0 || r.left >= vw) continue;
      let block = parent;
      while (block && block !== document.body && getComputedStyle(block).display.startsWith('inline')) block = block.parentElement;
      const piece = clean(value);
      if (parts.length) parts.push(block !== last ? '\n' : ' ');
      parts.push(piece);
      size += piece.length + 1;
      last = block;
    }
    text = parts.join('').slice(0, o.max);
    total = text.length;
  } else {
    const full = (document.body ? document.body.innerText : '') || '';
    total = full.length;
    text = full.slice(o.offset, o.offset + o.max);
  }
  return {
    title: clean(document.title).slice(0, 300), text, total, elements,
    scroll: { y: Math.round(root.scrollTop), height: Math.round(root.scrollHeight), viewport: vh },
  };
}

// pageFind runs inside a page frame, like pageCollect.
function pageFind(o) {
  const needle = o.text.toLowerCase();
  const walker = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT);
  const found = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const value = n.nodeValue;
    const parent = n.parentElement;
    if (!value || !parent || /^(SCRIPT|STYLE|NOSCRIPT|TEMPLATE)$/.test(parent.tagName)) continue;
    const at = value.toLowerCase().indexOf(needle);
    if (at < 0) continue;
    if (!parent.getClientRects().length || getComputedStyle(parent).visibility === 'hidden') continue;
    found.push({ node: n, at, parent });
    if (found.length >= 200) break;
  }
  const index = Math.min(o.index, Math.max(found.length - 1, 0));
  const pick = found[index];
  if (pick) pick.parent.scrollIntoView({ block: 'center', inline: 'nearest' });
  const snippets = found.slice(0, 5).map(f => {
    const v = f.node.nodeValue.replace(/\s+/g, ' ');
    const start = Math.max(0, f.at - 60);
    return v.slice(start, f.at + o.text.length + 80).trim();
  });
  return { count: found.length, index: pick ? index : -1, snippets };
}

async function snapshot(page, { scope, offset, max }) {
  const frames = page.frames();
  const main = await frames[0].evaluate(pageCollect, { scope, offset, max, prefix: '', limit: scope === 'viewport' ? 60 : 80 });
  const children = [];
  for (const [index, frame] of frames.entries()) {
    if (index === 0 || index > 8 || frame.url() === 'about:blank') continue;
    try {
      const info = await frame.evaluate(pageCollect, { scope: 'viewport', offset: 0, max: Math.min(1500, max), prefix: 'f' + index, limit: 30 });
      if (info.text || info.elements.length) children.push({ frame: index, url: frame.url(), text: info.text, elements: info.elements });
    } catch { /* A frame that navigated away or blocks evaluation is skipped. */ }
  }
  return { url: page.url(), ...main, frames: children };
}

// settle gives a page a moment to finish loading and rendering after an action.
async function settle(page) {
  await page.waitForLoadState('domcontentloaded', { timeout: 5000 }).catch(() => {});
  await page.waitForLoadState('load', { timeout: 3000 }).catch(() => {});
  await page.waitForTimeout(250);
}

const KEYS = new Set(['Enter', 'Tab', 'Escape', 'Space', 'Backspace', 'Delete', 'ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'PageDown', 'PageUp', 'Home', 'End']);
const CHANGES_PAGE = new Set(['click_element', 'click_position', 'fill_field', 'press_key', 'select_option']);

function makeSession(config, chromium) {
  let context;
  let next = 1;
  let active = 0;
  const tabs = new Map();
  let notes = [];
  const note = text => { if (notes.length < 5) notes.push(text); };
  const take = () => { const out = notes; notes = []; return out; };
  const register = page => {
    if (tabs.size >= 8) { void page.close().catch(() => {}); return; }
    const id = next++;
    tabs.set(id, page);
    active = id;
    page.on('close', () => { tabs.delete(id); if (active === id) active = [...tabs.keys()].pop() || 0; });
    page.on('dialog', dialog => {
      note('A ' + dialog.type() + ' dialog was dismissed: ' + dialog.message().replace(/\s+/g, ' ').slice(0, 200));
      void dialog.dismiss().catch(() => {});
    });
  };
  async function launch() {
    if (context) return;
    for (const name of ['profile', 'inbox', 'screenshots'])
      await fs.mkdir(path.join(config.dir, name), { recursive: true, mode: 0o700 });
    try {
      context = await chromium.launchPersistentContext(path.join(config.dir, 'profile'), {
        headless: config.headless, chromiumSandbox: config.sandbox,
        proxy: { server: config.proxy, username: config.username, password: config.password, bypass: '<-loopback>' },
        serviceWorkers: 'block', acceptDownloads: false, viewport: { width: 1280, height: 800 },
        timeout: config.timeout,
        args: ['--disable-dev-shm-usage', '--disable-quic', '--disable-background-networking',
          '--force-webrtc-ip-handling-policy=disable_non_proxied_udp', '--disable-extensions',
          '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'],
      });
    } catch (error) { throw new LaunchFailure(error); }
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

  function frameOf(page, index) {
    const frame = page.frames()[index || 0];
    if (!frame) throw new UserError('That frame does not exist. Read the page again.');
    return frame;
  }

  // target resolves a ref such as "e3" or "f1e7", or a selector inside a frame.
  function target(page, args) {
    const timeout = Math.min(config.timeout, 10000);
    if (args.ref) {
      const m = /^(?:f(\d+))?e(\d+)$/.exec(args.ref);
      if (!m) throw new UserError('That ref is not valid. Use a ref such as e3 from read_page.');
      const frame = page.frames()[m[1] ? Number(m[1]) : 0];
      if (!frame) throw new UserError('That ref belongs to a frame that is gone. Read the page again.');
      return { locator: frame.locator('[data-kest-ref="' + args.ref + '"]'), timeout };
    }
    if (args.selector) return { locator: frameOf(page, args.frame).locator(args.selector), timeout };
    throw new UserError('A ref or selector is required.');
  }

  // verify makes sure a ref still points at the element the owner approved.
  async function verify(locator, args, timeout) {
    if (!args.ref || args.expect === undefined || args.expect === null) return;
    const same = await locator.evaluate((el, expect) => {
      const f = window.__kest && window.__kest.labelOf;
      return Boolean(f) && f(el) === expect;
    }, args.expect, { timeout });
    if (!same) throw new UserError('The page changed since it was read, so that ref no longer points at the same element. Read the page again.');
  }

  const tabList = async () => {
    const result = [];
    for (const [tab, p] of tabs) result.push({ tab, url: p.url(), title: (await p.title()).slice(0, 300), active: tab === active });
    return result;
  };

  async function run(action, args) {
    await launch();
    if (action === 'list_tabs') return JSON.stringify({ tabs: await tabList(), notes: take() });

    let id = args.tab || active;
    if (args.tab && !tabs.has(args.tab)) throw new UserError('That tab does not exist. Use list_tabs.');
    if (action === 'navigate_page' && (args.new_tab || !id)) {
      if (tabs.size >= 8) throw new UserError('Eight tabs are open already. Close one first.');
      const fresh = await context.newPage();
      id = [...tabs.entries()].find(([, p]) => p === fresh)?.[0] || active;
    }
    if (!id) throw new UserError('No tab is open. Navigate to a page first.');
    const page = tabs.get(id);
    if (!page) throw new UserError('That tab does not exist. Use list_tabs.');
    if (active !== id) { active = id; await page.bringToFront().catch(() => {}); }
    const before = page.url();
    let scope = 'viewport';
    const offset = args.offset || 0;
    const extra = {};

    switch (action) {
      case 'navigate_page': {
        const response = await page.goto(publicURL(args.url), { waitUntil: 'domcontentloaded' });
        await settle(page);
        if (response && response.headers()['x-kest-blocked'])
          throw new UserError('That address is not reachable from here: private, local and non-web destinations are blocked, and the host may be down.');
        if (response && response.status() >= 400) note('The server answered HTTP ' + response.status() + '.');
        scope = 'page';
        break;
      }
      case 'read_page':
        scope = args.scope === 'viewport' ? 'viewport' : 'page';
        break;
      case 'navigate_history': {
        const options = { waitUntil: 'domcontentloaded' };
        if (args.direction === 'forward') await page.goForward(options);
        else if (args.direction === 'reload') await page.reload(options);
        else await page.goBack(options);
        await settle(page);
        scope = 'page';
        break;
      }
      case 'scroll_page': {
        const probe = () => page.evaluate(() => {
          const r = document.scrollingElement || document.documentElement;
          return r.scrollTop + ':' + r.scrollHeight + ':' + (document.body ? document.body.innerText.length : 0);
        });
        const signature = async () => (await snapshot(page, { scope: 'viewport', offset: 0, max: 300 })).text.slice(0, 300);
        const was = await probe();
        const textBefore = await signature();
        if (args.ref || args.selector) {
          const { locator, timeout } = target(page, args);
          await locator.scrollIntoViewIfNeeded({ timeout });
        } else if (args.direction === 'top') {
          await page.evaluate(() => (document.scrollingElement || document.documentElement).scrollTo(0, 0));
        } else if (args.direction === 'bottom') {
          await page.evaluate(() => { const r = document.scrollingElement || document.documentElement; r.scrollTo(0, r.scrollHeight); });
        } else {
          const step = args.pixels ? Math.abs(args.pixels) : 680;
          const delta = args.direction === 'up' ? -step : step;
          await page.mouse.move(640, 400);
          await page.mouse.wheel(0, delta);
          await page.waitForTimeout(350);
          if ((await probe()) === was && (await signature()) === textBefore) {
            // Something under the pointer swallowed the wheel; scroll the page itself.
            await page.evaluate(d => (document.scrollingElement || document.documentElement).scrollBy(0, d), delta);
          }
        }
        await page.waitForTimeout(300);
        extra.moved = (await probe()) !== was || (await signature()) !== textBefore;
        break;
      }
      case 'find_text': {
        extra.find = await frameOf(page, args.frame).evaluate(pageFind, { text: args.text, index: args.index || 0 });
        await page.waitForTimeout(250);
        break;
      }
      case 'click_element': {
        const { locator, timeout } = target(page, args);
        await verify(locator, args, timeout);
        await locator.click({ timeout });
        await settle(page);
        break;
      }
      case 'click_position':
        await page.mouse.click(args.x, args.y);
        await settle(page);
        break;
      case 'fill_field': {
        const { locator, timeout } = target(page, args);
        await verify(locator, args, timeout);
        await locator.fill(args.text, { timeout });
        if (args.submit) { await locator.press('Enter', { timeout }); await settle(page); }
        break;
      }
      case 'press_key': {
        if (!KEYS.has(args.key)) throw new UserError('That key is not allowed.');
        if (args.ref || args.selector) {
          const { locator, timeout } = target(page, args);
          await verify(locator, args, timeout);
          await locator.press(args.key, { timeout });
        } else await page.keyboard.press(args.key);
        await settle(page);
        break;
      }
      case 'select_option': {
        const { locator, timeout } = target(page, args);
        await verify(locator, args, timeout);
        try { await locator.selectOption({ value: args.value }, { timeout: 2000 }); }
        catch { await locator.selectOption({ label: args.value }, { timeout }); }
        await settle(page);
        break;
      }
      case 'hover_element': {
        const { locator, timeout } = target(page, args);
        await verify(locator, args, timeout);
        await locator.hover({ timeout });
        await page.waitForTimeout(300);
        break;
      }
      case 'wait_for': {
        const limit = Math.min(args.wait_ms || 5000, 15000);
        if (args.ref || args.selector) {
          const { locator } = target(page, args);
          await locator.waitFor({ state: args.state || 'visible', timeout: limit });
        } else if (args.text) {
          await page.getByText(args.text).first().waitFor({ state: args.state === 'hidden' ? 'hidden' : 'visible', timeout: limit });
        } else await page.waitForTimeout(Math.min(limit, 10000));
        break;
      }
      case 'upload_file': {
        const { locator, timeout } = target(page, args);
        await locator.setInputFiles(await inboxFile(config.dir, args.file), { timeout });
        break;
      }
      case 'screenshot': {
        const file = path.join(config.dir, 'screenshots', crypto.randomUUID() + '.png');
        const byElement = Boolean(args.ref || args.selector);
        if (args.full_page && !byElement) {
          const height = await page.evaluate(() => (document.scrollingElement || document.documentElement).scrollHeight);
          if (height > (args.max_height || 8000))
            throw new UserError('The page is ' + height + ' px tall, too tall for one image. Capture the viewport or one element, scrolling between shots.');
        }
        if (byElement) {
          const { locator, timeout } = target(page, args);
          const bounds = await locator.boundingBox({ timeout });
          if (!bounds || bounds.width > 4096 || bounds.height > 8000 || bounds.width * bounds.height > 10000000)
            throw new UserError('That element is missing or too large for an image.');
          await locator.screenshot({ path: file, timeout: config.timeout });
        } else await page.screenshot({ path: file, fullPage: Boolean(args.full_page), timeout: config.timeout });
        await fs.chmod(file, 0o600);
        return JSON.stringify({ tab: id, url: page.url(), title: (await page.title()).slice(0, 300), screenshot: file, notes: take() });
      }
      case 'close_tab': {
        await page.close();
        return JSON.stringify({ closed: id, tabs: await tabList(), notes: take() });
      }
      default: throw new UserError('Unknown browser action.');
    }

    const now = tabs.get(active);
    if (!now) return JSON.stringify({ tab: 0, notes: take().concat('No tabs are open.') });
    // A click that opens a popup or navigates is a new view: show it from the top.
    const opened = active !== id;
    if (opened || (CHANGES_PAGE.has(action) && now.url() !== before)) scope = 'page';
    if (opened) await settle(now);
    const info = await snapshot(now, { scope, offset: scope === 'page' && !opened ? offset : 0, max: config.maxChars });
    return JSON.stringify({ tab: active, opened_tab: opened, scope, offset: scope === 'page' ? offset : 0, notes: take(), ...extra, page: info });
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
      } catch (error) {
        // Raw Playwright errors include page content and typed secrets, so only
        // messages written in this file reach the model.
        process.stdout.write(JSON.stringify({ error: friendly(error) }) + '\n');
      }
    }).catch(() => { process.stdout.write(JSON.stringify({ error: 'Browser runtime unavailable. Check Playwright installation and Chromium sandbox support.' }) + '\n'); });
  });
}

module.exports = { publicURL, inboxFile, makeSession, launchError, friendly, UserError };
if (require.main === module || process.argv[1] === undefined) void main();
