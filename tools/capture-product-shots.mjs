// Captures product screenshots of the real, running Njira Vault interface for
// use on the homepage. These are photographs of the actual product — nothing
// here mocks up or invents an interface.
//
// Requires a running server and a Playwright install (not a project dependency):
//   go run .  &&  node tools/capture-product-shots.mjs [baseURL]
import { chromium } from '/opt/node22/lib/node_modules/playwright/index.mjs';

// Run the server with the production BASE_URL before capturing, so the links
// in the screenshots read as the real product rather than a dev origin.
const base = process.argv[2] || 'http://localhost:8080';
const out = new URL('../web/static/', import.meta.url).pathname;

const browser = await chromium.launch({ args: ['--no-sandbox'] });

// Wide and narrow captures of the same interface. The narrow pair is what
// small screens get, so the product stays readable there instead of being
// scaled down into a smudge.
const variants = [
  { suffix: '', width: 900, height: 1150 },
  { suffix: '-narrow', width: 430, height: 1000 },
];

for (const v of variants) {
  const ctx = await browser.newContext({
    viewport: { width: v.width, height: v.height },
    deviceScaleFactor: 2,
  });
  const page = await ctx.newPage();

  await page.goto(`${base}/vault`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(400);

  // 1. The create form, exactly as a visitor first meets it.
  await page.locator('.card').first().screenshot({ path: `${out}shot-vault-create${v.suffix}.png` });

  // 2. The result state, driven through the real API.
  await page.fill('#secret', 'AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\nAWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY');
  await page.selectOption('#kind', 'credentials');
  await page.selectOption('#expiry', '3600');
  await page.selectOption('#views', '1');
  await page.click('#create');
  await page.waitForSelector('#result:not([hidden])');
  await page.waitForTimeout(400);
  // Clear focus and the text selection so the shot shows the link, not a
  // highlight rectangle.
  await page.evaluate(() => {
    document.getElementById('secret-link').blur();
    window.getSelection().removeAllRanges();
  });
  await page.locator('.card').first().screenshot({ path: `${out}shot-vault-link${v.suffix}.png` });

  // Destroy the secret that was created for the screenshot.
  const url = await page.inputValue('#secret-link');
  const id = url.split('/s/')[1].split('#')[0];
  await page.evaluate((id) => fetch('/api/burn', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id }),
  }), id);

  await ctx.close();
}

// A full-viewport capture at desktop width, for the homepage's product panel.
// The element crops above are the right shape beside a column of text; a panel
// running the full width needs the product in its own chrome.
{
  const ctx = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    deviceScaleFactor: 2,
  });
  const page = await ctx.newPage();
  await page.goto(`${base}/vault`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${out}shot-vault-wide.png` });
  await ctx.close();
}

await browser.close();
console.log('captured wide, narrow and panel Njira Vault shots');
