# Vulpine Mark — Visual Element Labeling for AI Browser Agents

## What It Is

A standalone tool that annotates browser screenshots with numbered labels on interactive elements, returning both the annotated image and a structured element map. Enables AI agents to say "click @14" instead of guessing coordinates.

## Why It's Needed

AI agents using screenshots for visual grounding have 20-30% lower accuracy than agents with element-level labels (Microsoft SoM paper, 2023). Current approaches require heavy ML models (OmniParser needs 11GB VRAM, runs on A100 GPUs) or are tightly coupled to specific frameworks (WebArena, Vercel agent-browser). Nobody has shipped a fast, standalone, browser-agnostic tool.

## Competitive Landscape

| Tool | Approach | GPU Required | Standalone | Browser Support |
|------|----------|-------------|------------|-----------------|
| Microsoft SoM | SAM segmentation | Yes (heavy) | No (research demo) | Any (image-based) |
| OmniParser | YOLOv8 + Florence-2 | Yes (11GB VRAM) | Partially | Any (image-based) |
| SeeClick | 7B VLM | Yes (GPU) | No | Any (image-based) |
| WebArena SoM | JS injection | No | No (embedded in benchmark) | Chrome only |
| Anthropic CU | Raw coordinate prediction | No (cloud) | No (proprietary) | Any (screenshot) |
| **Vulpine Mark** | DOM layout tree + compositor | **No** | **Yes** | **Any via CDP/Juggler** |

## Our Unique Advantage

VulpineOS controls the browser engine. We can:
- Read the live layout tree (element positions) with zero ML overhead
- Render labels natively via the compositor (no JS injection, works on cross-origin iframes)
- Map labels directly to Juggler element handles — no coordinate guessing
- Run in <10ms (just reading existing layout + painting overlays)
- Handle dynamic content naturally (re-reads live DOM on each call)

## Architecture

```
Browser Page
     │
     ▼
Vulpine Mark Engine
├── 1. Enumerate interactive elements (buttons, links, inputs, selects)
│      via accessibility tree or DOM querySelectorAll
├── 2. Get bounding rects (getBoundingClientRect for each)
├── 3. Filter: only visible, in viewport, not obscured
├── 4. Assign labels (@1, @2, @3...)
├── 5. Render annotated screenshot:
│      - Take base screenshot
│      - Draw numbered badges at element centers
│      - Color-code by role (green=button, blue=link, purple=input)
├── 6. Return:
│      - Annotated PNG image
│      - JSON map: { "@1": {tag, role, text, x, y, w, h}, "@2": ... }
└── 7. Labels persist for subsequent click/type actions
```

## Implementation Options

### Option A: Juggler-Level (VulpineOS-native, fastest)
New Juggler method `Page.getAnnotatedScreenshot` that:
1. Reads the accessibility tree (already filtered by Phase 1)
2. Gets bounding rects for each interactive node
3. Takes a screenshot via the compositor
4. Draws labels using Gecko's canvas painting APIs
5. Returns PNG + element map in one call

**Pros:** Fastest (<10ms), works on cross-origin iframes, no JS injection.
**Cons:** Tied to Camoufox/VulpineOS. Needs Juggler JS code (not C++).

### Option B: Standalone Go Library (browser-agnostic)
A Go library that:
1. Takes any CDP connection (foxbridge, Chrome, etc.)
2. Calls `Runtime.evaluate` to get element positions
3. Takes screenshot via `Page.captureScreenshot`
4. Draws labels using Go image library
5. Returns annotated PNG + element map

**Pros:** Works with any browser. Can be a standalone repo.
**Cons:** Slightly slower (JS eval + image processing in Go). Can't handle cross-origin iframes.

### Option C: Both
Implement Option A in VulpineOS for maximum speed, and Option B as an open-source standalone library for the community. The standalone version drives adoption; the native version is the competitive advantage.

## Recommended Approach

**Build both. Open-source the standalone Go library. Keep the Juggler-native version in VulpineOS.**

The standalone `vulpine-mark` repo gets community adoption and positions VulpineOS as the ecosystem leader. The native version in VulpineOS is faster and more capable (cross-origin iframe support, no JS injection).

## Open Source: YES

**Repo:** `VulpineOS/vulpine-mark` (public)

Reasons:
- The concept (SoM) is already published research — no proprietary secret
- Community adoption drives traffic to VulpineOS
- The Go library works with Chrome too — wider audience
- The competitive advantage is the *native* Juggler implementation, not the standalone library

## MVP Scope (1-2 weeks)

1. Go library that connects via CDP WebSocket
2. Enumerates interactive elements via `Runtime.evaluate`
3. Gets bounding rects
4. Takes screenshot
5. Draws numbered badges using Go `image/draw`
6. Returns annotated PNG + JSON element map
7. CLI tool: `vulpine-mark --cdp ws://localhost:9222 --output annotated.png`
8. MCP tool integration: `vulpine_annotated_screenshot` in VulpineOS

## API Design

```go
// Standalone library
mark := vulpinemark.New(cdpWebSocketURL)
result, err := mark.Annotate()
// result.Image - annotated PNG bytes
// result.Elements - map of label → {tag, role, text, x, y, w, h}
// result.Labels - ordered list of labels

// Click by label
mark.Click("@3") // clicks the element labeled @3
```

```bash
# CLI
vulpine-mark --cdp ws://localhost:9222 --output screenshot.png --json elements.json
```

## Integration with VulpineOS MCP

New tool `vulpine_annotated_screenshot`:
- Returns annotated image as `image` content block
- Returns element map as `text` content block
- Agent sees labeled screenshot + structured data
- Can then call `vulpine_click` with coordinates from the map

## Success Metrics

- Accuracy improvement on WebArena-style benchmarks (target: 20%+ vs non-annotated)
- Latency: <100ms for standalone, <20ms for native Juggler
- GitHub stars / adoption by other agent frameworks

## Future Ideas (overnight ideation)

These are NEW ideas beyond the MVP and current roadmap, ranked by impact-to-effort ratio. The top items are the ones most worth building next.

### 1. Semantic Focus Modes (`--focus=forms|nav|content|cta`)
**What:** Let the agent request a scoped annotation pass ("only form fields", "only primary CTAs", "only nav links") so dense pages aren't swamped with 80+ badges.
**Effort:** ~4 hours. It's a filter stage on top of `enumerate.go` — role whitelist + heuristics (CTA = button with prominent bg color + above-fold).
**Why it matters:** On real sites (checkout flows, dashboards) raw SoM produces unreadable label soup and tanks accuracy *worse* than no labels. Focus modes are the single biggest usability lever for agents and cost almost nothing to add.
**Where:** Public repo (library API + CLI flag). Native version inherits it free.

### 2. Overlap-Aware Label Fanout with Leader Lines
**What:** Detect when badges would overlap (simple rect collision on the label rects, not the element rects), then push colliding labels outward and draw a thin leader line from the badge back to the element.
**Effort:** ~6 hours. Greedy repulsion pass over label positions + `image/draw` line primitive. No layout engine needed.
**Why it matters:** Current top-left placement is unreadable on dense UIs (toolbars, data grids, menu bars). This is the #1 visual complaint from every SoM implementation in the wild and is trivially solvable without ML.
**Where:** Public repo. Pure rendering concern.

### 3. Diff Mode (`Annotate(before, after)`)
**What:** Take two annotated snapshots and label *only* elements that appeared, moved, or changed state between them. Returns a third image highlighting deltas in a distinct color.
**Effort:** ~5 hours. Element map already has stable keys (tag+role+text+rect hash); diff is a set-compare + re-render.
**Why it matters:** Agents constantly re-annotate after every click just to find "what's new" (a modal, a dropdown, an error message). Diff mode turns an O(page) problem into O(change) — dramatically fewer tokens sent to the LLM, and huge reliability win for modal detection.
**Where:** Public repo. Builds directly on existing element map.

### 4. Cluster Mode for Repeated Items (`@5[0..n]`)
**What:** Detect visually/structurally repeated children (list rows, card grids, search results) and collapse them under a single cluster label. Agent addresses children as `@5[3]` instead of getting 40 separate badges.
**Effort:** ~10 hours. Sibling similarity heuristic (same tag path + same bounding-rect shape within parent) + indexed label schema + click-by-index helper.
**Why it matters:** Kills the "100 labels on a product grid" problem. Agents can reason about "the 3rd result" semantically and the label map shrinks ~10x on list-heavy pages. This is the single feature that would make vulpine-mark obviously better than every competitor on e-commerce/search tasks.
**Where:** Public repo. Pure DOM-side logic.

### 5. SVG Overlay Output (`--format=svg`)
**What:** Emit an SVG overlay (not a raster PNG) that can be composited over the raw screenshot client-side. Agent/UI can toggle the overlay, zoom losslessly, or extract just the underlying screenshot on demand.
**Effort:** ~4 hours. Replace `image/draw` calls with an SVG writer; keep the raster path as the default.
**Why it matters:** Solves two pain points at once: (a) VLMs sometimes reason better on the *unannotated* image then consult the overlay separately, (b) UI tools want a toggleable layer. Also dramatically smaller payload than PNG for sparse pages.
**Where:** Public repo. Trivially portable.

---

### Also-ran ideas (not written above, logged for later)

6. **Heatmap mode** — replace labels with a color gradient showing element "importance" (size × above-fold × role) for quick visual triage. ~6h. Public.
7. **Palette packs** — high-contrast / colorblind-safe / monochrome / dark-mode palette profiles selectable via `--palette`. ~2h. Public.
8. **Record-and-replay sequences** — capture annotated screenshots + element maps on every agent action, persist as a timeline for post-hoc debugging. ~8h. **VulpineOS-native** (ties into `internal/recording/`).
9. **Occlusion-aware labeling** — run `elementFromPoint` at each candidate's center and drop elements hidden behind modals/overlays. Already on roadmap but worth flagging as a correctness (not ideation) priority. ~3h. Public.
10. **Scroll-stitched full-page mode with virtualized-list guard** — detect virtualized scrollers (React Virtual, Ag-Grid) and bail out instead of infinite-scrolling. ~8h. Public.
11. **Cross-origin iframe labeling via compositor** — the native-Juggler killer feature: label elements inside cross-origin iframes by reading their layout tree from the parent process. ~16h. **VulpineOS-native only** (impossible in CDP standalone).
12. **Label-stability across re-renders** — hash each element's semantic identity (role+text+ancestor chain) so `@5` refers to the same button on re-annotate, even if DOM order changed. ~6h. Public. Huge for multi-step flows.
13. **Arrow mode** — draw arrows from each element to a list of labels anchored at the screen edge, keeping the page content unobscured. ~5h. Public.
14. **Pre-warmed annotation cache** — on navigation, pre-compute annotation in the background so the first `Annotate()` call after page-load is instant. ~4h. **VulpineOS-native** (needs lifecycle hooks).
15. **Confidence scores per label** — expose a confidence field in the element map (`0.0-1.0`) based on visibility %, occlusion, and role certainty; lets the agent ignore low-confidence badges. ~3h. Public.
