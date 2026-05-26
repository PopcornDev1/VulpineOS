import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { chromium } from 'playwright-core'
import { encode } from 'gpt-tokenizer'
import { getVulpineOptimizedDOM } from './serializer.mjs'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const DEFAULT_FIXTURES = [
  'fixtures/retail-product.html',
  'fixtures/retail-category.html',
  'fixtures/docs-reference.html',
]
const QUALITY_EXPECTATIONS = {
  'fixtures/retail-product.html': [
    'FoxForge RTX 5060 Ti',
    'Search products',
    'In stock',
    'Buying options',
    'Retailer offer 1',
    '$730.99',
    'Add retailer 1 to cart',
    'Specifications',
    'Customer reviews',
    'Contact support',
  ],
  'fixtures/retail-category.html': [
    'Graphics cards and creator GPUs',
    'Brand',
    'Weekend upgrade event',
    'View all bundles',
    'GPU result 1',
    'Compare result 1',
    'Open product 1',
    'Subscribe to stock alerts',
  ],
  'fixtures/docs-reference.html': [
    'Browser automation API reference',
    'API reference',
    'vulpine.method.1',
    'parameter_1',
    'Try method 1',
    'Copy API example',
  ],
}
const QUALITY_MINIMUMS = {
  'fixtures/retail-product.html': { minRefs: 85, minHeadings: 10 },
  'fixtures/retail-category.html': { minRefs: 50, minHeadings: 13 },
  'fixtures/docs-reference.html': { minRefs: 70, minHeadings: 100 },
}

function parseArgs(argv) {
  const args = {
    output: path.join(__dirname, 'results', 'latest.json'),
    chromePath: process.env.CHROME_PATH || '',
    fixtures: DEFAULT_FIXTURES,
    maxNodes: 180,
    maxDepth: 10,
    maxTextLength: 90,
    failOnQuality: true,
  }
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--output') args.output = argv[++i]
    else if (arg === '--chrome-path') args.chromePath = argv[++i]
    else if (arg === '--fixture') args.fixtures.push(argv[++i])
    else if (arg === '--max-nodes') args.maxNodes = Number(argv[++i])
    else if (arg === '--max-depth') args.maxDepth = Number(argv[++i])
    else if (arg === '--max-text-length') args.maxTextLength = Number(argv[++i])
    else if (arg === '--no-fail-on-quality') args.failOnQuality = false
    else if (arg === '--help') {
      console.log(`Usage: node benchmarks/token-optimization/benchmark.mjs [options]

Options:
  --output <path>           JSON result path
  --chrome-path <path>      Chrome/Chromium executable path
  --fixture <path>          Add an extra fixture path or URL
  --max-nodes <n>           Optimized DOM node cap (default: 180)
  --max-depth <n>           Optimized DOM depth cap (default: 10)
  --max-text-length <n>     Optimized DOM text cap per node (default: 90)
  --no-fail-on-quality      Report missing expected strings without exiting non-zero
`)
      process.exit(0)
    }
  }
  return args
}

function findChrome(explicit) {
  const candidates = [
    explicit,
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Chromium.app/Contents/MacOS/Chromium',
    '/usr/bin/google-chrome',
    '/usr/bin/google-chrome-stable',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
  ].filter(Boolean)

  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate
  }
  throw new Error('Chrome/Chromium executable not found. Set CHROME_PATH or pass --chrome-path.')
}

function fixtureURL(value) {
  if (/^https?:\/\//.test(value) || value.startsWith('file://')) return value
  const absolute = path.isAbsolute(value) ? value : path.join(__dirname, value)
  return pathToFileURL(absolute).href
}

function tokenCount(value) {
  return encode(typeof value === 'string' ? value : JSON.stringify(value)).length
}

function compactChromeAX(tree) {
  return tree.nodes.map((node) => {
    const item = [
      Number(node.nodeId),
      node.role?.value || '',
      node.name?.value || '',
    ]
    const props = {}
    for (const prop of node.properties || []) {
      if (['disabled', 'expanded', 'checked', 'selected', 'required', 'focused'].includes(prop.name)) {
        props[prop.name] = prop.value?.value
      }
    }
    if (Object.keys(props).length) item.push(props)
    if (node.childIds?.length) item.push(node.childIds.map(Number))
    return item
  })
}

function mean(values) {
  return Math.round(values.reduce((sum, value) => sum + value, 0) / values.length)
}

function reduction(base, optimized) {
  return Number((((base - optimized) / base) * 100).toFixed(1))
}

function qualityForFixture(fixture, snapshot) {
  const expected = QUALITY_EXPECTATIONS[fixture] || []
  const minimums = QUALITY_MINIMUMS[fixture] || {}
  const haystack = JSON.stringify(snapshot).toLowerCase()
  const missing = expected.filter((value) => !haystack.includes(value.toLowerCase()))
  const refCount = snapshot.nodes.filter((node) => typeof node[node.length - 1] === 'string' && node[node.length - 1].startsWith('@')).length
  const headingCount = snapshot.nodes.filter((node) => /^h[1-6]$/.test(node[1])).length
  const coverageFailures = []
  if (minimums.minRefs && refCount < minimums.minRefs)
    coverageFailures.push(`refs ${refCount}/${minimums.minRefs}`)
  if (minimums.minHeadings && headingCount < minimums.minHeadings)
    coverageFailures.push(`headings ${headingCount}/${minimums.minHeadings}`)
  return {
    expected: expected.length,
    matched: expected.length - missing.length,
    missing,
    refCount,
    headingCount,
    coverageFailures,
    pass: missing.length === 0 && coverageFailures.length === 0,
  }
}

async function measureFixture(browser, fixture, options) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1200 } })
  const url = fixtureURL(fixture)
  await page.goto(url, { waitUntil: 'networkidle' })

  const rawHTML = await page.evaluate(() => document.documentElement.outerHTML)
  const ariaSnapshot = await page.locator('body').ariaSnapshot()
  const cdp = await page.context().newCDPSession(page)
  const chromeAX = await cdp.send('Accessibility.getFullAXTree')
  const vulpine = await page.evaluate(
    ({ serializerSource, opts }) => {
      const source = serializerSource
        .replace(/export function getVulpineOptimizedDOM/, 'function getVulpineOptimizedDOM')
        .replace(/export /g, '')
      const getSnapshot = Function(`${source}; return getVulpineOptimizedDOM;`)()
      return getSnapshot(opts)
    },
    {
      serializerSource: fs.readFileSync(path.join(__dirname, 'serializer.mjs'), 'utf8'),
      opts: {
        maxDepth: options.maxDepth,
        maxNodes: options.maxNodes,
        maxTextLength: options.maxTextLength,
      },
    },
  )

  await page.close()

  const counts = {
    rawHTML: tokenCount(rawHTML),
    chromeAXVerbose: tokenCount(chromeAX),
    chromeAXCompact: tokenCount(compactChromeAX(chromeAX)),
    playwrightAria: tokenCount(ariaSnapshot),
    vulpineOptimized: tokenCount(vulpine.snapshot),
  }

  return {
    fixture,
    tokens: counts,
    reductions: {
      vsRawHTML: reduction(counts.rawHTML, counts.vulpineOptimized),
      vsChromeAXVerbose: reduction(counts.chromeAXVerbose, counts.vulpineOptimized),
      vsChromeAXCompact: reduction(counts.chromeAXCompact, counts.vulpineOptimized),
      vsPlaywrightAria: reduction(counts.playwrightAria, counts.vulpineOptimized),
    },
    optimizedNodes: vulpine.snapshot.nodes.length,
    truncated: vulpine.truncated,
    quality: qualityForFixture(fixture, vulpine.snapshot),
  }
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  const chromePath = findChrome(args.chromePath)
  const browser = await chromium.launch({
    executablePath: chromePath,
    headless: true,
  })

  try {
    const fixtures = args.fixtures.map((fixture) => fixture.replace(`${__dirname}/`, ''))
    const results = []
    for (const fixture of fixtures) results.push(await measureFixture(browser, fixture, args))

    const aggregate = {
      rawHTML: mean(results.map((result) => result.tokens.rawHTML)),
      chromeAXVerbose: mean(results.map((result) => result.tokens.chromeAXVerbose)),
      chromeAXCompact: mean(results.map((result) => result.tokens.chromeAXCompact)),
      playwrightAria: mean(results.map((result) => result.tokens.playwrightAria)),
      vulpineOptimized: mean(results.map((result) => result.tokens.vulpineOptimized)),
    }
    const payload = {
      generatedAt: new Date().toISOString(),
      system: {
        platform: `${os.type()} ${os.release()} ${os.arch()}`,
        node: process.version,
        chromePath,
      },
      options: {
        maxDepth: args.maxDepth,
        maxNodes: args.maxNodes,
        maxTextLength: args.maxTextLength,
      },
      aggregate: {
        tokens: aggregate,
        reductions: {
          vsRawHTML: reduction(aggregate.rawHTML, aggregate.vulpineOptimized),
          vsChromeAXVerbose: reduction(aggregate.chromeAXVerbose, aggregate.vulpineOptimized),
          vsChromeAXCompact: reduction(aggregate.chromeAXCompact, aggregate.vulpineOptimized),
          vsPlaywrightAria: reduction(aggregate.playwrightAria, aggregate.vulpineOptimized),
        },
      },
      fixtures: results,
    }

    fs.mkdirSync(path.dirname(args.output), { recursive: true })
    fs.writeFileSync(args.output, JSON.stringify(payload, null, 2) + '\n')

    console.log('Token optimization benchmark')
    console.log(`Chrome: ${chromePath}`)
    console.table({
      'Raw HTML': aggregate.rawHTML,
      'Chrome full AX tree (verbose CDP)': aggregate.chromeAXVerbose,
      'Chrome full AX tree (compact)': aggregate.chromeAXCompact,
      'Playwright ariaSnapshot': aggregate.playwrightAria,
      'VulpineOS optimized DOM': aggregate.vulpineOptimized,
    })
    console.table(payload.aggregate.reductions)
    console.table(Object.fromEntries(results.map((result) => [
      result.fixture,
      `${result.quality.matched}/${result.quality.expected} strings, ${result.quality.refCount} refs, ${result.quality.headingCount} headings`,
    ])))
    console.log(`Wrote ${path.resolve(args.output)}`)

    const failedQuality = results.filter((result) => !result.quality.pass)
    if (args.failOnQuality && failedQuality.length) {
      for (const result of failedQuality) {
        const failures = []
        if (result.quality.missing.length)
          failures.push(`missing strings: ${result.quality.missing.join(', ')}`)
        failures.push(...result.quality.coverageFailures)
        console.error(`${result.fixture} quality check failed: ${failures.join('; ')}`)
      }
      process.exitCode = 1
    }
  } finally {
    await browser.close()
  }
}

main().catch((error) => {
  console.error(error)
  process.exit(1)
})
