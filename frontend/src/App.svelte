<script>
  import { onMount, onDestroy } from "svelte";
  import { WindowSetSize, WindowSetMinSize, EventsOn, WindowSetPosition, WindowGetPosition } from "../wailsjs/runtime/runtime.js";
  import {
    GetConfig,
    GetCertInfo,
    GetRotatorStatus,
    GetUDPStatus,
    RotatorSetFollow,
    RotatorPark,
    RotatorGoto,
    RadioSetFreq,
    RadioSetTxFreq,
  } from "../wailsjs/go/main/App.js";
  import StatusTab from "./components/StatusTab.svelte";
  import ConfigTab from "./components/ConfigTab.svelte";
  import MiniMode from "./components/MiniMode.svelte";
  import CertBanner from "./components/CertBanner.svelte";

  // ── Window constants ───────────────────────────────────────────────────────
  const WIDTH       = 430;
  const FULL_HEIGHT = 620;
  const MINI_BASE   = 130;  // no rotator
  const MINI_ROT    = 185;  // + rotator (az/el + follow control)
  const MINI_MAP    = 295;  // + compact polar map

  // ── UI state ───────────────────────────────────────────────────────────────
  let miniMode  = localStorage.getItem("ui.miniMode") === "true";
  let activeTab = "status";
  let utcTime   = "";
  let clockInterval;

  // ── Shared runtime state ───────────────────────────────────────────────────
  let freqMHz       = "";
  let mode          = "";
  let split         = false;
  let freqTxMHz     = "";
  let modeTx        = "";
  let statusMsg     = "";
  let qsoResult     = null;
  let radioEnabled  = false;
  let rotatorEnabled = false;
  let rotConnected  = false;
  let rotMoving     = false;
  let rotAz         = 0;
  let rotEl         = 0;
  let rotFollow     = "off";
  let hfAz          = null;
  let satAz         = null;
  let satEl         = null;
  let demandedAz    = null;
  let demandedEl    = null;
  let minimapEnabled = false;
  let certInfo      = null;

  // ── Reactive mini-height ───────────────────────────────────────────────────
  $: miniHeight = rotatorEnabled
    ? (minimapEnabled ? MINI_MAP : MINI_ROT)
    : MINI_BASE;

  async function setWindowSizeAnchored(width, height) {
    const pos = await WindowGetPosition();
    WindowSetSize(width, height);
    WindowSetPosition(pos.x, pos.y);
  }

  // Re-apply window size when height changes while in mini mode
  $: if (miniMode) {
    WindowSetMinSize(WIDTH, miniHeight);
    setWindowSizeAnchored(WIDTH, miniHeight);
  }

  // ── Window helpers ─────────────────────────────────────────────────────────
  function enterMiniMode() {
    miniMode = true;
    localStorage.setItem("ui.miniMode", "true");
    WindowSetMinSize(WIDTH, miniHeight);
    setWindowSizeAnchored(WIDTH, miniHeight);
  }

  function exitMiniMode() {
    miniMode = false;
    localStorage.setItem("ui.miniMode", "false");
    WindowSetMinSize(WIDTH, FULL_HEIGHT);
    setWindowSizeAnchored(WIDTH, FULL_HEIGHT);
  }

  // ── Rotator actions ────────────────────────────────────────────────────────
  async function setFollow(followMode) {
    rotFollow = followMode;
    await RotatorSetFollow(followMode);
  }

  async function park() {
    rotFollow = "off";
    await RotatorPark();
  }

  function mhzStrToHz(mhzStr) {
    const [intPart, decPart = "00000"] = mhzStr.split(".");
    const padded = (decPart + "00000").slice(0, 5);
    return parseInt(intPart) * 1_000_000 + parseInt(padded) * 10;
  }

  // Local accumulators so rapid scrolling doesn't re-derive from the stale
  // radio-polled freqMHz (which updates only once per second).
  let _localFreqHz = null;
  let _localTxFreqHz = null;
  let _resetFreqTimer = null;
  let _resetTxFreqTimer = null;

  function handleFreqScroll({ detail }) {
    if (!freqMHz) return;
    if (_localFreqHz === null) _localFreqHz = mhzStrToHz(freqMHz);
    _localFreqHz += detail.deltaHz;
    if (_localFreqHz <= 0) return;
    RadioSetFreq(_localFreqHz);
    clearTimeout(_resetFreqTimer);
    _resetFreqTimer = setTimeout(() => { _localFreqHz = null; }, 1500);
  }

  function handleTxFreqScroll({ detail }) {
    if (!freqTxMHz) return;
    if (_localTxFreqHz === null) _localTxFreqHz = mhzStrToHz(freqTxMHz);
    _localTxFreqHz += detail.deltaHz;
    if (_localTxFreqHz <= 0) return;
    RadioSetTxFreq(_localTxFreqHz);
    clearTimeout(_resetTxFreqTimer);
    _resetTxFreqTimer = setTimeout(() => { _localTxFreqHz = null; }, 1500);
  }

  // ── Rotator scroll/click ────────────────────────────────────────────────────
  // _localRot* tracks the last commanded position so rapid scroll events
  // accumulate correctly without waiting for the polled rotAz/rotEl to update.
  // Re-synced from poll only after 5 s of inactivity.
  let _localRotAz = null;
  let _localRotEl = null;
  let _lastRotCmdTime = 0;

  function handleRotScroll({ detail }) {
    if (!rotConnected) return;
    if (_localRotAz === null) _localRotAz = rotAz;
    if (_localRotEl === null) _localRotEl = rotEl;
    _localRotAz = ((_localRotAz + (detail.deltaAz || 0)) % 360 + 360) % 360;
    _localRotEl = Math.max(0, Math.min(90, _localRotEl + (detail.deltaEl || 0)));
    RotatorGoto(_localRotAz, _localRotEl);
    demandedAz = _localRotAz;
    demandedEl = _localRotEl;
    _lastRotCmdTime = Date.now();
  }

  function handleRotGoto({ detail }) {
    if (!rotConnected) return;
    RotatorGoto(detail.az, detail.el);
    _localRotAz = detail.az;
    _localRotEl = detail.el;
    demandedAz = detail.az;
    demandedEl = detail.el;
    _lastRotCmdTime = Date.now();
  }

  // ── Clock ──────────────────────────────────────────────────────────────────
  function updateClock() {
    const now = new Date();
    utcTime = now.toUTCString().split(" ").slice(4, 5)[0] + " UTC";
  }

  // ── Lifecycle ──────────────────────────────────────────────────────────────
  let offRadio, offQso, offStatus, offRotPos, offRotStatus, offRotBearing,
      offRotMoving, offRotFollow, offRotGoto, offProfile, offRadioEnabled, offRotEnabled, offAdvanced, offCert;

  onMount(async () => {
    updateClock();
    clockInterval = setInterval(updateClock, 1000);

    offRadio = EventsOn("radio:status", (data) => {
      if (data && data.freqMHz !== undefined) {
        freqMHz   = Number(data.freqMHz).toFixed(5);
        mode      = data.mode || "";
        split     = data.split || false;
        freqTxMHz = data.split ? Number(data.freqTxMHz).toFixed(5) : "";
        modeTx    = data.split ? (data.modeTx || "") : "";
      }
    });
    offQso = EventsOn("qso:result", (data) => {
      qsoResult = data;
      setTimeout(() => { qsoResult = null; }, 30000);
    });
    offStatus = EventsOn("status:message", (msg) => { statusMsg = msg; });
    offRotPos = EventsOn("rotator:position", (data) => {
      if (data) {
        rotAz = data.az; rotEl = data.el;
        // Re-sync local accumulators once 5 s have passed since the last command,
        // so the next scroll starts from the actual position rather than a stale target.
        if (Date.now() - _lastRotCmdTime > 5000) {
          _localRotAz = null;
          _localRotEl = null;
          demandedAz = null;
          demandedEl = null;
        }
      }
    });
    offRotStatus = EventsOn("rotator:status", (connected) => { rotConnected = connected; });
    offRotMoving = EventsOn("rotator:moving", (moving) => { rotMoving = moving; });
    offRotBearing = EventsOn("rotator:bearing", (data) => {
      if (!data) return;
      if (data.type === "hf") { hfAz = data.az; }
      if (data.type === "sat") { satAz = data.az; satEl = data.el; }
    });
    offRotEnabled = EventsOn("rotator:enabled", (enabled) => {
      rotatorEnabled = enabled;
      if (!enabled) rotConnected = false;
    });
    offRadioEnabled = EventsOn("radio:enabled", (enabled) => { radioEnabled = enabled; });
    offProfile = EventsOn("profile:switched", (data) => {
      rotatorEnabled = data?.rotatorEnabled || false;
      radioEnabled   = data?.radioEnabled   || false;
      hfAz = null; satAz = null; satEl = null;
      rotFollow = "off"; rotConnected = false;
    });
    offAdvanced = EventsOn("advanced:changed", (data) => {
      minimapEnabled = data?.minimapEnabled ?? false;
    });
    offCert = EventsOn("cert:install_needed", (data) => { certInfo = data; });
    offRotFollow = EventsOn("rotator:followmode", (mode) => { rotFollow = mode; });
    offRotGoto = EventsOn("rotator:goto", (data) => {
      if (data) { demandedAz = data.az; demandedEl = data.el; _lastRotCmdTime = Date.now(); }
    });

    // Load initial state
    const cfg = await GetConfig();
    const p = cfg.profiles?.[cfg.profile];
    radioEnabled   = p?.flrig_ena || p?.hamlib_ena || false;
    rotatorEnabled = p?.rotator_enabled || false;
    if (rotatorEnabled) {
      const s = await GetRotatorStatus();
      rotConnected = s.connected;
      rotAz = s.az; rotEl = s.el;
      rotFollow = s.followMode || "off";
    }

    const adv = await GetUDPStatus();
    minimapEnabled = adv.minimapEnabled;

    try {
      const ci = await GetCertInfo();
      if (!ci.isInstalled) certInfo = ci;
    } catch (e) {
      // bridge not ready yet — retry once after a short delay
      setTimeout(async () => {
        try {
          const ci = await GetCertInfo();
          if (!ci.isInstalled) certInfo = ci;
        } catch (_) {}
      }, 500);
    }

    // Apply correct window size on startup
    if (miniMode) {
      WindowSetMinSize(WIDTH, miniHeight);
      WindowSetSize(WIDTH, miniHeight);
    }
  });

  onDestroy(() => {
    clearInterval(clockInterval);
    if (offRadio)      offRadio();
    if (offQso)        offQso();
    if (offStatus)     offStatus();
    if (offRotPos)     offRotPos();
    if (offRotStatus)  offRotStatus();
    if (offRotMoving)  offRotMoving();
    if (offRotFollow)  offRotFollow();
    if (offRotGoto)    offRotGoto();
    if (offRotBearing) offRotBearing();
    if (offProfile)    offProfile();
    if (offRotEnabled) offRotEnabled();
    if (offRadioEnabled) offRadioEnabled();
    if (offAdvanced)   offAdvanced();
    if (offCert)       offCert();
  });
</script>

<div class="flex flex-col h-screen">
  {#if miniMode}
    <!-- ── MINI MODE ─────────────────────────────────────────────────────── -->
    <MiniMode
      {utcTime}
      {freqMHz} {mode} {split} {freqTxMHz} {modeTx} {qsoResult}
      {rotatorEnabled} {minimapEnabled}
      {rotConnected} {rotMoving} {rotAz} {rotEl} {rotFollow}
      {hfAz} {satAz} {satEl} {demandedAz} {demandedEl}
      on:expand={exitMiniMode}
      on:follow={(e) => setFollow(e.detail)}
      on:freqscroll={handleFreqScroll}
      on:txfreqscroll={handleTxFreqScroll}
      on:rotscroll={handleRotScroll}
      on:rotgoto={handleRotGoto}
    />
  {:else}
    <!-- ── FULL MODE ──────────────────────────────────────────────────────── -->
    <header class="bg-surface-header flex items-center justify-between px-3 h-10 flex-shrink-0 border-b border-stroke-subtle">
      <div class="flex items-center gap-1 bg-surface-app rounded-lg p-1">
        <button
          class="flex-1 text-center text-2xs py-1 px-4 cursor-pointer rounded-md border-0 transition-colors duration-150
            {activeTab === 'status'
            ? 'bg-surface-input text-fg-bright font-semibold'
            : 'bg-transparent text-fg-secondary hover:text-fg-base'}"
          on:click={() => (activeTab = "status")}>Status</button>
        <button
          class="flex-1 text-center text-2xs py-1 px-4 cursor-pointer rounded-md border-0 transition-colors duration-150
            {activeTab === 'config'
            ? 'bg-surface-input text-fg-bright font-semibold'
            : 'bg-transparent text-fg-secondary hover:text-fg-base'}"
          on:click={() => (activeTab = "config")}>Configuration</button>
      </div>
      <div class="flex items-center gap-2">
        <div class="text-2xs text-fg-muted font-mono">{utcTime}</div>
        <!-- Mini-mode toggle button -->
        <button
          class="flex items-center justify-center w-6 h-6 rounded-md border border-stroke-base text-fg-bright hover:bg-surface-input transition-colors duration-150"
          title="Mini mode"
          on:click={enterMiniMode}
        ><i class="fa-solid fa-compress text-xs"></i></button>
      </div>
    </header>

    <CertBanner {certInfo} />

    <main class="flex-1 overflow-y-auto overflow-x-hidden">
      <div class:hidden={activeTab !== "status"}>
        <StatusTab
          {freqMHz} {mode} {split} {freqTxMHz} {modeTx} {statusMsg} {qsoResult}
          {radioEnabled} {rotatorEnabled}
          {rotConnected} {rotMoving} {rotAz} {rotEl} {rotFollow}
          {hfAz} {satAz} {satEl} {demandedAz} {demandedEl}
          on:follow={(e) => setFollow(e.detail)}
          on:park={park}
          on:freqscroll={handleFreqScroll}
          on:txfreqscroll={handleTxFreqScroll}
          on:rotscroll={handleRotScroll}
          on:rotgoto={handleRotGoto}
        />
      </div>
      <div class:hidden={activeTab !== "config"}><ConfigTab /></div>
    </main>
  {/if}
</div>

<style>
  :global(.hidden) {
    display: none !important;
  }
</style>
