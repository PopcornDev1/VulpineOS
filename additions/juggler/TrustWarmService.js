/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

"use strict";

const {setTimeout, clearTimeout} = ChromeUtils.importESModule('resource://gre/modules/Timer.sys.mjs');

const DEFAULT_SITES = [
  {url: 'https://www.google.com/', weight: 3, maxVisitsPerDay: 5},
  {url: 'https://www.youtube.com/', weight: 2, maxVisitsPerDay: 3},
  {url: 'https://www.wikipedia.org/', weight: 1, maxVisitsPerDay: 2},
  {url: 'https://www.reddit.com/', weight: 1, maxVisitsPerDay: 2},
  {url: 'https://www.amazon.com/', weight: 1, maxVisitsPerDay: 2},
  {url: 'https://news.ycombinator.com/', weight: 1, maxVisitsPerDay: 1},
  {url: 'https://github.com/', weight: 1, maxVisitsPerDay: 1},
];

// --- Bezier trajectory generation (JS port of MouseTrajectories.hpp) ---

function factorial(n) {
  let r = 1;
  for (let i = 2; i <= n; i++) r *= i;
  return r;
}

function binomial(n, k) {
  return factorial(n) / (factorial(k) * factorial(n - k));
}

function bernsteinPolynomialPoint(x, i, n) {
  return binomial(n, i) * Math.pow(x, i) * Math.pow(1 - x, n - i);
}

function bernsteinPolynomial(points, t) {
  const n = points.length - 1;
  let x = 0, y = 0;
  for (let i = 0; i <= n; i++) {
    const bern = bernsteinPolynomialPoint(t, i, n);
    x += points[i][0] * bern;
    y += points[i][1] * bern;
  }
  return [x, y];
}

function easeOutQuad(n) {
  return -n * (n - 2);
}

function gaussianRandom(mean, stddev) {
  let u = 0, v = 0;
  while (u === 0) u = Math.random();
  while (v === 0) v = Math.random();
  const z = Math.sqrt(-2.0 * Math.log(u)) * Math.cos(2.0 * Math.PI * v);
  return mean + z * stddev;
}

function generateTrajectory(fromX, fromY, toX, toY) {
  const leftBound = Math.min(fromX, toX) - 80;
  const rightBound = Math.max(fromX, toX) + 80;
  const downBound = Math.min(fromY, toY) - 80;
  const upBound = Math.max(fromY, toY) + 80;

  // 2 random internal knots
  const knots = [];
  for (let i = 0; i < 2; i++) {
    knots.push([
      leftBound + Math.random() * (rightBound - leftBound),
      downBound + Math.random() * (upBound - downBound),
    ]);
  }

  // Control points: [from, knot1, knot2, to]
  const controlPoints = [[fromX, fromY], ...knots, [toX, toY]];

  // Calculate curve points
  const midPtsCnt = Math.max(Math.abs(fromX - toX), Math.abs(fromY - toY), 2);
  const curvePoints = [];
  for (let i = 0; i < midPtsCnt; i++) {
    const t = i / (midPtsCnt - 1);
    curvePoints.push(bernsteinPolynomial(controlPoints, t));
  }

  // Distort with Gaussian noise
  const distorted = [curvePoints[0]];
  for (let i = 1; i < curvePoints.length - 1; i++) {
    let delta = 0;
    if (Math.random() < 0.5) {
      delta = Math.round(gaussianRandom(1.0, 1.0));
    }
    distorted.push([curvePoints[i][0], curvePoints[i][1] + delta]);
  }
  distorted.push(curvePoints[curvePoints.length - 1]);

  // Tween with easeOutQuad
  let totalLength = 0;
  for (let i = 1; i < distorted.length; i++) {
    const dx = distorted[i][0] - distorted[i - 1][0];
    const dy = distorted[i][1] - distorted[i - 1][1];
    totalLength += Math.sqrt(dx * dx + dy * dy);
  }
  const targetPoints = Math.min(150, Math.max(2, Math.round(Math.pow(totalLength, 0.25) * 20)));

  const result = [];
  for (let i = 0; i < targetPoints; i++) {
    const t = i / (targetPoints - 1);
    const easedT = easeOutQuad(t);
    const index = Math.min(Math.round(easedT * (distorted.length - 1)), distorted.length - 1);
    result.push({
      x: Math.round(distorted[index][0]),
      y: Math.round(distorted[index][1]),
    });
  }
  return result;
}

// --- TrustWarmService ---

export class TrustWarmService {
  constructor(targetRegistry, dispatcher) {
    this._targetRegistry = targetRegistry;
    this._dispatcher = dispatcher;
    this._state = 'stopped';
    this._sites = DEFAULT_SITES;
    this._browserContextId = undefined;
    this._cooldownMinutes = 5;
    this._intensity = 'medium';
    this._visitHistory = new Map();
    this._sitesWarmed = 0;
    this._currentSite = null;
    this._lastVisit = null;
    this._currentTimer = null;
    this._warmingTargetId = null;
    this._stateChangedCallback = null;
  }

  setStateChangedCallback(callback) {
    this._stateChangedCallback = callback;
  }

  _emitStateChanged() {
    if (this._stateChangedCallback) {
      this._stateChangedCallback({
        state: this._state,
        currentSite: this._currentSite,
      });
    }
  }

  async start(config = {}) {
    if (!Services.prefs.getBoolPref('vulpineos.trustwarm.enabled', false))
      throw new Error('Trust warming is disabled. Set vulpineos.trustwarm.enabled to true.');

    if (config.sites && config.sites.length > 0)
      this._sites = config.sites;
    if (config.browserContextId !== undefined)
      this._browserContextId = config.browserContextId;
    if (config.cooldownMinutes !== undefined)
      this._cooldownMinutes = config.cooldownMinutes;
    if (config.interactionIntensity)
      this._intensity = config.interactionIntensity;

    this._state = 'idle';
    this._emitStateChanged();
  }

  async stop() {
    this._state = 'stopped';
    if (this._currentTimer) {
      clearTimeout(this._currentTimer);
      this._currentTimer = null;
    }
    await this._closeWarmingTab();
    this._currentSite = null;
    this._emitStateChanged();
  }

  getStatus() {
    return {
      state: this._state,
      sitesWarmed: this._sitesWarmed,
      currentSite: this._currentSite || undefined,
      lastVisit: this._lastVisit || undefined,
    };
  }

  async notifyIdle() {
    if (this._state !== 'idle')
      return;
    this._state = 'warming';
    this._emitStateChanged();
    try {
      await this._runWarmingLoop();
    } catch (e) {
      dump(`TrustWarm: warming loop error: ${e.message}\n`);
    }
    if (this._state === 'warming' || this._state === 'pausing') {
      this._state = 'idle';
      this._emitStateChanged();
    }
  }

  async notifyBusy() {
    if (this._state === 'warming')
      this._state = 'pausing';
    // The warming loop checks state at each step and will yield
  }

  dispose() {
    this.stop();
  }

  // --- Internal ---

  async _runWarmingLoop() {
    while (this._state === 'warming') {
      const site = this._pickNextSite();
      if (!site) {
        // All sites visited recently
        return;
      }

      this._currentSite = site.url;
      this._emitStateChanged();

      try {
        await this._warmSite(site);
        this._recordVisit(site.url);
        this._sitesWarmed++;
      } catch (e) {
        dump(`TrustWarm: error warming ${site.url}: ${e.message}\n`);
      }

      this._currentSite = null;
      await this._closeWarmingTab();

      if (this._state !== 'warming')
        break;

      // Inter-site cooldown
      await this._dwell(5000, 15000);
    }
  }

  async _warmSite(site) {
    // Create a warming tab
    const targetId = await this._targetRegistry.newPage({
      browserContextId: this._browserContextId,
    });
    this._warmingTargetId = targetId;

    // Find the target
    let target = null;
    for (const t of this._targetRegistry.targets()) {
      if (t.id() === targetId) {
        target = t;
        break;
      }
    }
    if (!target)
      throw new Error('Failed to find warming target');

    // Navigate
    const browsingContext = target._linkedBrowser.browsingContext;
    const loadURIOptions = {
      triggeringPrincipal: Services.scriptSecurityManager.getSystemPrincipal(),
    };
    browsingContext.loadURI(Services.io.newURI(site.url), loadURIOptions);

    // Wait for load
    await this._dwell(3000, 6000);
    if (this._state !== 'warming') return;

    // Get viewport dimensions
    const win = target._window;
    const viewportWidth = win.innerWidth || 1280;
    const viewportHeight = win.innerHeight || 720;

    // Intensity determines interaction count
    const scrollCount = this._intensity === 'light' ? 2 : this._intensity === 'heavy' ? 5 : 3;
    const hoverCount = this._intensity === 'light' ? 1 : this._intensity === 'heavy' ? 3 : 2;

    // Scroll sequence
    let mouseX = Math.round(viewportWidth / 2);
    let mouseY = Math.round(viewportHeight / 3);

    for (let i = 0; i < scrollCount && this._state === 'warming'; i++) {
      // Move mouse to random position
      const targetX = this._randomInt(100, viewportWidth - 100);
      const targetY = this._randomInt(100, viewportHeight - 100);

      const trajectory = generateTrajectory(mouseX, mouseY, targetX, targetY);
      await this._dispatchTrajectory(target, trajectory);
      mouseX = targetX;
      mouseY = targetY;

      if (this._state !== 'warming') return;
      await this._dwell(500, 2000);

      // Scroll down
      const scrollDelta = Math.round(gaussianRandom(350, 100));
      const scrollSteps = this._randomInt(3, 8);
      for (let s = 0; s < scrollSteps && this._state === 'warming'; s++) {
        const delta = Math.round(scrollDelta / scrollSteps * this._randomFloat(0.7, 1.3));
        this._dispatchWheelToWindow(win, mouseX, mouseY, delta);
        await this._dwell(30, 80);
      }

      if (this._state !== 'warming') return;
      await this._dwell(1000, 3000);
    }

    // Hover sequence
    for (let i = 0; i < hoverCount && this._state === 'warming'; i++) {
      const targetX = this._randomInt(80, viewportWidth - 80);
      const targetY = this._randomInt(80, viewportHeight - 80);
      const trajectory = generateTrajectory(mouseX, mouseY, targetX, targetY);
      await this._dispatchTrajectory(target, trajectory);
      mouseX = targetX;
      mouseY = targetY;
      await this._dwell(300, 1500);
    }

    // Optional click (30% chance)
    if (this._state === 'warming' && Math.random() < 0.3) {
      const clickX = this._randomInt(100, viewportWidth - 100);
      const clickY = this._randomInt(100, viewportHeight - 200);
      const trajectory = generateTrajectory(mouseX, mouseY, clickX, clickY);
      await this._dispatchTrajectory(target, trajectory);

      this._dispatchMouseToWindow(win, 'mousedown', clickX, clickY, 0);
      await this._dwell(50, 150);
      this._dispatchMouseToWindow(win, 'mouseup', clickX, clickY, 0);

      // Dwell on result
      await this._dwell(2000, 4000);
    }

    // Final dwell
    await this._dwell(1000, 3000);
  }

  async _dispatchTrajectory(target, trajectory) {
    const win = target._window;
    for (let i = 0; i < trajectory.length && this._state === 'warming'; i++) {
      this._dispatchMouseToWindow(win, 'mousemove', trajectory[i].x, trajectory[i].y, 0);
      // Delay proportional to trajectory density (~4ms per point)
      if (i < trajectory.length - 1) {
        await new Promise(r => {
          this._currentTimer = setTimeout(() => {
            this._currentTimer = null;
            r();
          }, this._randomInt(2, 8));
        });
      }
    }
  }

  _dispatchMouseToWindow(win, type, x, y, button) {
    try {
      const utils = win.windowUtils;
      if (utils && utils.sendMouseEvent) {
        utils.sendMouseEvent(type, x, y, button, 1, 0);
      }
    } catch (e) {
      // Window may have been closed
    }
  }

  _dispatchWheelToWindow(win, x, y, deltaY) {
    try {
      const utils = win.windowUtils;
      if (utils && utils.sendWheelEvent) {
        utils.sendWheelEvent(
          x, y,
          0, deltaY, 0,        // deltaX, deltaY, deltaZ
          0,                     // modifiers
          0, 0,                  // lineOrPageDeltaX, lineOrPageDeltaY
          0,                     // options
          0, 0, 0, 0            // overflowDeltaX/Y, lineOrPageDeltaX/Y
        );
      }
    } catch (e) {
      // Window may have been closed
    }
  }

  async _closeWarmingTab() {
    if (this._warmingTargetId) {
      try {
        for (const t of this._targetRegistry.targets()) {
          if (t.id() === this._warmingTargetId) {
            t.close();
            break;
          }
        }
      } catch (e) {
        // Tab may already be closed
      }
      this._warmingTargetId = null;
    }
  }

  // --- Site selection ---

  _pickNextSite() {
    const eligible = this._sites.filter(s => this._shouldVisitSite(s));
    if (eligible.length === 0)
      return null;

    // Weighted random selection
    const totalWeight = eligible.reduce((sum, s) => sum + (s.weight || 1), 0);
    let r = Math.random() * totalWeight;
    for (const site of eligible) {
      r -= (site.weight || 1);
      if (r <= 0)
        return site;
    }
    return eligible[eligible.length - 1];
  }

  _shouldVisitSite(site) {
    const history = this._visitHistory.get(site.url) || [];
    const now = Date.now();
    const dayAgo = now - 24 * 60 * 60 * 1000;
    const recentVisits = history.filter(v => v.timestamp > dayAgo);

    if (recentVisits.length >= (site.maxVisitsPerDay || 3))
      return false;

    // 30-minute minimum between same-site visits
    const lastVisit = recentVisits[recentVisits.length - 1];
    if (lastVisit && (now - lastVisit.timestamp) < 30 * 60 * 1000)
      return false;

    return true;
  }

  _recordVisit(url) {
    const now = Date.now();
    if (!this._visitHistory.has(url))
      this._visitHistory.set(url, []);
    this._visitHistory.get(url).push({timestamp: now});
    this._lastVisit = now;
  }

  // --- Randomization helpers ---

  async _dwell(minMs, maxMs) {
    const mean = (minMs + maxMs) / 2;
    const stddev = (maxMs - minMs) / 6;
    const duration = Math.round(
      Math.min(maxMs * 1.5, Math.max(minMs * 0.5, gaussianRandom(mean, stddev)))
    );
    return new Promise(resolve => {
      this._currentTimer = setTimeout(() => {
        this._currentTimer = null;
        resolve();
      }, Math.max(1, duration));
    });
  }

  _randomInt(min, max) {
    return Math.floor(Math.random() * (max - min + 1)) + min;
  }

  _randomFloat(min, max) {
    return min + Math.random() * (max - min);
  }
}
