import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const previewSource = await readFile(new URL('../src/components/ScreenPreview.tsx', import.meta.url), 'utf8');
const previewStyles = await readFile(new URL('../src/components/ScreenPreview.css', import.meta.url), 'utf8');

test('desktop fullscreen uses the Wails window API and keeps the preview above the app layout', () => {
  assert.match(previewSource, /WindowFullscreen/);
  assert.match(previewSource, /WindowUnfullscreen/);
  assert.match(previewSource, /WindowIsFullscreen/);
  assert.match(previewStyles, /\.screen-preview\.is-fullscreen\s*\{[\s\S]*position:\s*fixed/);
});
