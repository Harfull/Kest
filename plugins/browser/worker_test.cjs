const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { publicURL, inboxFile, launchError } = require('./worker.cjs');

test('launch diagnostics identify runtime failures without exposing raw values', () => {
  for (const [input, expected] of [
    ['No usable sandbox! secret-token', 'sandbox is unavailable'],
    ['Check failed: sys_chroot("/proc/self/fdinfo/") == 0 secret-token', 'sandbox is unavailable'],
    ['Failed to move to new namespace: Operation not permitted secret-token', 'sandbox is unavailable'],
    ["Executable doesn't exist: /private/secret-token", 'executable is missing'],
    ['error while loading shared libraries: secret-token', 'system libraries are missing'],
    ['Missing X server or $DISPLAY secret-token', 'has no display'],
    ['ProcessSingleton secret-token', 'profile is already in use'],
    ['Permission denied secret-token', 'cannot access its files'],
    ['unexpected private browser failure secret-token', 'could not launch'],
  ]) {
    const output = launchError(new Error(input));
    assert.ok(output.includes(expected));
    assert.ok(!output.includes('secret-token'));
  }
});

test('navigation permits web URLs without credentials', () => {
  assert.equal(publicURL('https://example.com'), 'https://example.com/');
  for (const url of ['file:///etc/passwd', 'javascript:alert(1)', 'data:text/html,secret', 'https://token@example.com'])
    assert.throws(() => publicURL(url));
});

test('upload confinement checks traversal, size, directories and symlinks', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'kest-upload-'));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await fs.mkdir(path.join(root, 'inbox'));
  await fs.writeFile(path.join(root, 'inbox', 'owner.txt'), 'owner approved');
  await fs.writeFile(path.join(root, 'secret.txt'), 'private');
  const file = await inboxFile(root, 'owner.txt');
  assert.equal(file.buffer.toString(), 'owner approved');
  for (const name of ['../secret.txt', path.join(root, 'secret.txt'), '.', 'missing'])
    await assert.rejects(inboxFile(root, name));
  const handle = await fs.open(path.join(root, 'inbox', 'large'), 'w');
  await handle.truncate(10 * 1024 * 1024 + 1);
  await handle.close();
  await assert.rejects(inboxFile(root, 'large'));
  try {
    await fs.symlink(path.join(root, 'secret.txt'), path.join(root, 'inbox', 'link'));
    await assert.rejects(inboxFile(root, 'link'));
  } catch (err) {
    if (err.code !== 'EPERM') throw err;
    t.diagnostic('symlink creation requires privileges on this Windows host');
  }
});

test('worker errors are mapped to safe, useful messages', () => {
  const { friendly, UserError } = require('./worker.cjs');
  for (const [input, expected] of [
    ['locator.click: Error: strict mode violation: locator(\'a\') resolved to 3 elements: secret-token', 'matches 3 elements'],
    ['locator.click: Timeout 10000ms exceeded. element <div class="secret-token"> intercepts pointer events', 'covering the target'],
    ['page.goto: net::ERR_NAME_NOT_RESOLVED at https://secret-token.example/', 'ERR_NAME_NOT_RESOLVED'],
    ['page.goto: Timeout 30000ms exceeded. navigating to secret-token', 'did not finish loading'],
    ['locator.fill: Timeout 10000ms exceeded. waiting for locator secret-token', 'not found or could not be used'],
    ['Target page, context or browser has been closed', 'tab was closed'],
    ['something unexpected with secret-token', 'could not complete'],
  ]) {
    const output = friendly(new Error(input));
    assert.ok(output.includes(expected), output);
    assert.ok(!output.includes('secret-token'), output);
  }
  assert.equal(friendly(new UserError('A ref or selector is required.')), 'A ref or selector is required.');
});
