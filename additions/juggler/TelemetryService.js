/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

"use strict";

const {setTimeout, clearTimeout} = ChromeUtils.importESModule('resource://gre/modules/Timer.sys.mjs');

const TELEMETRY_INTERVAL_MS = 2000;

export class TelemetryService {
  constructor(targetRegistry, session) {
    this._targetRegistry = targetRegistry;
    this._session = session;
    this._timer = null;
    this._injectionCount = 0;
    this._detectionRiskScore = 0;
    // Event loop lag measurement state
    this._lastEventLoopLagMs = 0;
    this._lagSamples = [];
    this._lagSampleTimer = null;
  }

  start() {
    if (this._timer)
      return;
    this._startLagSampling();
    this._tick();
  }

  stop() {
    if (this._timer) {
      clearTimeout(this._timer);
      this._timer = null;
    }
    if (this._lagSampleTimer) {
      clearTimeout(this._lagSampleTimer);
      this._lagSampleTimer = null;
    }
  }

  /**
   * Measure main-thread event loop latency by scheduling a setTimeout(0)
   * and measuring the actual delay until the callback fires.
   * This correlates with CPU load — a busy main thread will have higher lag.
   */
  _startLagSampling() {
    const sampleLag = () => {
      const scheduled = Cu.now();
      this._lagSampleTimer = setTimeout(() => {
        const actual = Cu.now();
        const lagMs = actual - scheduled;
        this._lagSamples.push(lagMs);
        // Keep a rolling window of samples (one per ~500ms, keep last 8)
        if (this._lagSamples.length > 8)
          this._lagSamples.shift();
        if (this._timer)
          sampleLag();
      }, 0);
    };
    sampleLag();
  }

  _getEventLoopLagMs() {
    if (this._lagSamples.length === 0)
      return 0;
    const sum = this._lagSamples.reduce((a, b) => a + b, 0);
    return sum / this._lagSamples.length;
  }

  reportInjectionAttempt(details) {
    this._injectionCount++;
    // Increase detection risk when injection attempts are detected
    this._detectionRiskScore = Math.min(100, this._detectionRiskScore + 5);

    try {
      this._session.emitEvent('Browser.injectionAttemptDetected', {
        browserContextId: details.browserContextId || undefined,
        url: details.url || '',
        attemptType: details.attemptType || 'hidden-content',
        details: details.details || '',
        timestamp: Date.now(),
        blocked: details.blocked !== false,
      });
    } catch (e) {
      dump(`TelemetryService: error emitting injection event: ${e.message}\n`);
    }
  }

  getTelemetry() {
    return this._collectMetrics();
  }

  getContextTelemetry(browserContextId) {
    let pageCount = 0;
    const currentUrls = [];

    for (const target of this._targetRegistry.targets()) {
      if (target._browserContext && target._browserContext.browserContextId === browserContextId) {
        pageCount++;
        try {
          const url = target._linkedBrowser?.browsingContext?.currentURI?.spec;
          if (url)
            currentUrls.push(url);
        } catch (e) {
          // Browsing context may not be available
        }
      }
    }

    return {
      pageCount,
      trustWarmingState: 'unknown',
      detectionEvents: this._injectionCount,
      currentUrls,
    };
  }

  dispose() {
    this.stop();
  }

  _tick() {
    const metrics = this._collectMetrics();

    try {
      this._session.emitEvent('Browser.telemetryUpdate', metrics);
    } catch (e) {
      // Session may be disposed
    }

    // Decay detection risk over time
    this._detectionRiskScore = Math.max(0, this._detectionRiskScore - 0.5);

    this._timer = setTimeout(() => this._tick(), TELEMETRY_INTERVAL_MS);
  }

  _collectMetrics() {
    let memoryMB = 0;

    // Collect memory from memory reporter
    try {
      const memMgr = Cc["@mozilla.org/memory-reporter-manager;1"]
        .getService(Ci.nsIMemoryReporterManager);
      memoryMB = memMgr.resident / (1024 * 1024);
    } catch (e) {
      // Memory reporter may not be available
    }

    // Event loop lag: average delay (ms) of setTimeout(0) callbacks.
    // Higher lag indicates the main thread is under heavier CPU load.
    let eventLoopLagMs = 0;
    try {
      eventLoopLagMs = this._getEventLoopLagMs();
    } catch (e) {
      eventLoopLagMs = 0;
    }

    // Count active contexts and pages
    let activeContexts = 0;
    let activePages = 0;
    const seenContexts = new Set();

    for (const target of this._targetRegistry.targets()) {
      activePages++;
      if (target._browserContext) {
        seenContexts.add(target._browserContext.browserContextId);
      }
    }
    activeContexts = seenContexts.size;

    return {
      memoryMB: Math.round(memoryMB * 10) / 10,
      eventLoopLagMs: Math.round(eventLoopLagMs * 100) / 100,
      detectionRiskScore: Math.round(this._detectionRiskScore * 10) / 10,
      activeContexts,
      activePages,
      timestamp: Date.now(),
    };
  }
}
