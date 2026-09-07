const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch({ args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 1000, height: 900 } });
  const errors = [];
  page.on('console', (msg) => { if (msg.type() === 'error') errors.push(msg.text()); });
  page.on('pageerror', (e) => errors.push(String(e)));
  await page.goto('http://localhost:5183', { waitUntil: 'load' });
  await page.waitForTimeout(4000);
  await page.screenshot({ path: '/tmp/claude-1000/-home-qwezert-projects-pixi-node-game/74a1a6fd-f158-4b8f-8815-2f6bd0d96b4f/scratchpad/unitselect.png' });
  console.log('ERRORS:', errors.join('\n'));
  await browser.close();
})();
