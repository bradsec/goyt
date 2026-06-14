/**
 * goyt UI integration tests.
 * Requires a running server: GOYT_PORT=3000 ./goyt (or set TEST_BASE_URL).
 */

import puppeteer from 'puppeteer';

const BASE_URL = process.env.TEST_BASE_URL || 'http://localhost:3000';

const results = [];

function record(name, passed, detail = '') {
  results.push({ name, passed, detail });
  const mark = passed ? 'PASS' : 'FAIL';
  console.log(`${mark}  ${name}${detail ? ` (${detail})` : ''}`);
}

async function run() {
  const browser = await puppeteer.launch({
    headless: 'new',
    args: ['--no-sandbox', '--disable-setuid-sandbox'],
  });

  const page = await browser.newPage();
  const consoleErrors = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });
  page.on('pageerror', (err) => consoleErrors.push(err.message));

  try {
    // Page loads
    const response = await page.goto(BASE_URL, { waitUntil: 'networkidle0', timeout: 20000 });
    record('page loads with HTTP 200', response.status() === 200, `status ${response.status()}`);

    const title = await page.title();
    record('page title set', title.includes('goyt'), title);

    // Core form contract
    for (const id of ['url-input', 'type-select', 'quality-select', 'format-select', 'submit-button', 'download-form']) {
      const present = await page.$(`#${id}`) !== null;
      record(`#${id} present`, present);
    }

    // Submit disabled until a URL validates
    const initiallyDisabled = await page.$eval('#submit-button', (el) => el.disabled);
    record('submit disabled before input', initiallyDisabled);

    // Invalid URL keeps submit disabled
    await page.type('#url-input', 'not-a-url');
    await new Promise((r) => setTimeout(r, 1500));
    const stillDisabled = await page.$eval('#submit-button', (el) => el.disabled);
    record('submit stays disabled for invalid input', stillDisabled);

    // Ledger sections exist
    for (const id of ['downloading-section', 'processing-section', 'queued-section', 'completed-section', 'failed-section', 'empty-board']) {
      const present = await page.$(`#${id}`) !== null;
      record(`#${id} present`, present);
    }

    // Settings panel opens and exposes expected fields
    await page.click('#settings-toggle');
    await page.waitForSelector('#settings-panel.show', { timeout: 5000 });
    // Let the slide-in transition finish before interacting with the panel.
    await new Promise((r) => setTimeout(r, 400));
    record('settings panel opens', true);

    for (const name of ['download_path', 'max_concurrent_downloads', 'cookies_file_path', 'enable_hardware_acceleration']) {
      const present = await page.$(`#settings-form [name="${name}"]`) !== null;
      record(`settings field ${name} present`, present);
    }

    // Executable paths are no longer web-editable (security: API-5); the inputs
    // must not be present in the settings form.
    for (const name of ['yt_dlp_path', 'ffmpeg_path']) {
      const absent = await page.$(`#settings-form [name="${name}"]`) === null;
      record(`settings field ${name} removed`, absent);
    }

    await page.click('#settings-close');
    await new Promise((r) => setTimeout(r, 400));

    // Banner present and theme toggle removed
    const bannerPresent = await page.$('.banner') !== null;
    record('ascii banner present', bannerPresent);
    const themeToggleGone = await page.$('#theme-toggle') === null;
    record('theme toggle removed', themeToggleGone);

    // API reachable from page context
    const health = await page.evaluate(async () => {
      const res = await fetch('/api/health');
      return res.ok;
    });
    record('/api/health reachable', health);

    // Media player dialog present and closed on load
    const dialogState = await page.evaluate(() => {
      const d = document.getElementById('media-player-modal');
      return { exists: !!d, open: d ? d.open : true };
    });
    if (!dialogState.exists) throw new Error('media-player-modal dialog missing');
    if (dialogState.open) throw new Error('media-player-modal should be closed on load');
    record('media player dialog present and closed', true);

    // Mobile layout does not overflow horizontally
    await page.setViewport({ width: 390, height: 760 });
    await new Promise((r) => setTimeout(r, 400));
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1);
    record('no horizontal overflow at 390px', !overflow);

    // Console must be clean (polling noise excluded if server restarts mid-run)
    const realErrors = consoleErrors.filter((e) => !e.includes('ERR_CONNECTION_REFUSED'));
    record('no console errors', realErrors.length === 0, realErrors.slice(0, 3).join(' | '));
  } catch (err) {
    record('test run completed', false, err.message);
  } finally {
    await browser.close();
  }

  const failed = results.filter((r) => !r.passed);
  console.log(`\n${results.length - failed.length}/${results.length} passed`);
  process.exit(failed.length === 0 ? 0 : 1);
}

run();
