const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { publicURL, inboxFile } = require('./worker.cjs');

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
