<script>
  import { createEventDispatcher } from "svelte";
  const dispatch = createEventDispatcher();

  export let rotConnected = false;
  export let rotMoving = false;
  export let rotAz = 0;
  export let rotEl = 0;
  export let rotFollow = "off";
  export let hfAz = null;
  export let satAz = null;
  export let satEl = null;
  export let demandedAz = null;
  export let demandedEl = null;

  // Precompute current rotator position for the azimuth map.
  $: azRad = (rotAz - 90) * Math.PI / 180;
  $: elR   = 72 * (1 - rotEl / 90);

  let showMap = localStorage.getItem("rotator.showMap") === "true";
  function toggleMap() {
    showMap = !showMap;
    localStorage.setItem("rotator.showMap", String(showMap));
  }

  // Local target state — set immediately on click so the overlay doesn't
  // wait for the demandedAz prop to round-trip through App.svelte.
  let _targetAz = null;
  let _targetEl = null;
  let _targetTimer = null;
  $: _showAz = demandedAz ?? _targetAz;
  $: _showEl = demandedEl ?? _targetEl;

  function onAzWheel(e) {
    const delta = e.deltaY < 0 ? 1 : -1;
    dispatch("rotscroll", { deltaAz: delta, deltaEl: 0 });
  }

  function onElWheel(e) {
    const delta = e.deltaY < 0 ? 1 : -1;
    dispatch("rotscroll", { deltaAz: 0, deltaEl: delta });
  }

  function onMapClick(e) {
    const svg = e.currentTarget;
    const pt = svg.createSVGPoint();
    pt.x = e.clientX;
    pt.y = e.clientY;
    const svgPt = pt.matrixTransform(svg.getScreenCTM().inverse());
    const dx = svgPt.x - 80;
    const dy = svgPt.y - 80;
    const dist = Math.sqrt(dx * dx + dy * dy);
    if (dist > 72) return;
    const az = Math.round(((Math.atan2(dx, -dy) * 180 / Math.PI) + 360) % 360 * 10) / 10;
    const el = Math.round(Math.max(0, 90 * (1 - dist / 72)) * 10) / 10;
    _targetAz = az;
    _targetEl = el;
    clearTimeout(_targetTimer);
    _targetTimer = setTimeout(() => { _targetAz = null; _targetEl = null; }, 5000);
    dispatch("rotgoto", { az, el });
  }
</script>

<div class="bg-surface-card border border-stroke-subtle rounded-lg px-4 py-3 flex flex-col gap-3 font-mono">

  <!-- Header -->
  <div class="flex items-center justify-between">
    <span class="text-fg-muted text-2xs uppercase tracking-widest font-semibold">Rotator</span>
    <div class="flex items-center gap-2.5">
      <button
        class="text-2xs px-1.5 py-0.5 border-0 text-fg-dim hover:text-fg-base bg-transparent"
        on:click={toggleMap}
        title={showMap ? "Hide map" : "Show map"}
      >{showMap ? "▲ map" : "▼ map"}</button>
      {#if rotMoving}
        <span class="text-accent-orange text-2xs font-semibold tracking-wide animate-pulse">Moving…</span>
      {/if}
      <div class="flex items-center gap-1.5">
        <span class="w-1.5 h-1.5 rounded-full flex-shrink-0 {rotConnected ? 'bg-accent-green' : 'bg-fg-dim'}"></span>
        <span class="text-fg-dim text-2xs">{rotConnected ? "connected" : "disconnected"}</span>
      </div>
    </div>
  </div>

  <!-- Az / El instrument tiles -->
  <div class="flex gap-2">
    <div class="flex-1 bg-surface-app border border-stroke-section rounded-md px-3 py-2.5 flex flex-col gap-0.5"
      style="cursor:ns-resize" title="Scroll to adjust azimuth"
      on:wheel|preventDefault={onAzWheel}>
      <span class="text-fg-muted text-2xs uppercase tracking-wider">Azimuth</span>
      <span class="text-accent-value text-xl font-bold leading-tight">{rotAz.toFixed(1)}°</span>
    </div>
    <div class="flex-1 bg-surface-app border border-stroke-section rounded-md px-3 py-2.5 flex flex-col gap-0.5"
      style="cursor:ns-resize" title="Scroll to adjust elevation"
      on:wheel|preventDefault={onElWheel}>
      <span class="text-fg-muted text-2xs uppercase tracking-wider">Elevation</span>
      <span class="text-accent-value text-xl font-bold leading-tight">{rotEl.toFixed(1)}°</span>
    </div>
  </div>

  <!-- Azimuth map: polar plot (center = zenith, edge = horizon) -->
  {#if showMap}
  <div class="flex justify-center">
    <svg viewBox="0 0 160 160" width="160" height="160" xmlns="http://www.w3.org/2000/svg"
      style="cursor:crosshair" title="Click to point rotator" on:click={onMapClick}>
      <!-- Background -->
      <circle cx="80" cy="80" r="72" fill="#1e1e1e" stroke="#404040" stroke-width="1"/>

      <!-- Elevation rings: 60° and 30° above horizon -->
      <circle cx="80" cy="80" r="24" fill="none" stroke="#383838" stroke-width="0.75" stroke-dasharray="2,3"/>
      <circle cx="80" cy="80" r="48" fill="none" stroke="#383838" stroke-width="0.75" stroke-dasharray="2,3"/>

      <!-- Tick marks every 45° -->
      {#each [0, 45, 90, 135, 180, 225, 270, 315] as deg}
        {@const r = (deg % 90 === 0) ? 66 : 69}
        {@const rad = (deg - 90) * Math.PI / 180}
        <line
          x1={80 + r * Math.cos(rad)} y1={80 + r * Math.sin(rad)}
          x2={80 + 72 * Math.cos(rad)} y2={80 + 72 * Math.sin(rad)}
          stroke="#555555" stroke-width="1"
        />
      {/each}

      <!-- Cardinal labels -->
      <text x="80"  y="4"   text-anchor="middle"  dominant-baseline="hanging" fill="#777" font-size="9" font-family="monospace">N</text>
      <text x="156" y="80"  text-anchor="end"      dominant-baseline="middle"  fill="#777" font-size="9" font-family="monospace">E</text>
      <text x="80"  y="156" text-anchor="middle"  dominant-baseline="auto"    fill="#777" font-size="9" font-family="monospace">S</text>
      <text x="4"   y="80"  text-anchor="start"   dominant-baseline="middle"  fill="#777" font-size="9" font-family="monospace">W</text>

      <!-- HF bearing: dashed line — only in HF mode -->
      {#if rotFollow === "hf" && hfAz !== null}
        {@const rad = (Number(hfAz) - 90) * Math.PI / 180}
        <line
          x1="80" y1="80"
          x2={80 + 70 * Math.cos(rad)} y2={80 + 70 * Math.sin(rad)}
          stroke="#ffaa55" stroke-width="1.5" stroke-dasharray="4,3" stroke-linecap="round" opacity="0.65"
        />
      {/if}

      <!-- SAT position: polar dot (az + el) — only in SAT mode -->
      {#if rotFollow === "sat" && satAz !== null}
        {@const rad = (Number(satAz) - 90) * Math.PI / 180}
        {@const r = 72 * (1 - Number(satEl || 0) / 90)}
        <circle cx={80 + r * Math.cos(rad)} cy={80 + r * Math.sin(rad)} r="4" fill="#ffaa55" opacity="0.8"/>
      {/if}

      <!-- Demanded position: dashed orange line + hollow circle -->
      {#if _showAz !== null}
        {@const dRad = (Number(_showAz) - 90) * Math.PI / 180}
        {@const dR   = 72 * (1 - Number(_showEl || 0) / 90)}
        <line x1="80" y1="80" x2={80 + 70 * Math.cos(dRad)} y2={80 + 70 * Math.sin(dRad)}
          stroke="#ffaa55" stroke-width="1.5" stroke-dasharray="4,3" stroke-linecap="round" opacity="0.8"/>
        <circle cx={80 + dR * Math.cos(dRad)} cy={80 + dR * Math.sin(dRad)} r="5"
          fill="none" stroke="#ffaa55" stroke-width="1.5" opacity="0.9"/>
      {/if}

      <!-- Current rotator: faint needle + position dot -->
      <line
        x1="80" y1="80"
        x2={80 + 70 * Math.cos(azRad)} y2={80 + 70 * Math.sin(azRad)}
        stroke="#55aaff" stroke-width="1" opacity="0.25" stroke-linecap="round"
      />
      <circle cx={80 + elR * Math.cos(azRad)} cy={80 + elR * Math.sin(azRad)} r="5" fill="#55aaff"/>

      <!-- Zenith dot -->
      <circle cx="80" cy="80" r="2" fill="#555555"/>
    </svg>
  </div>
  {/if}

  <!-- Follow mode: segmented control -->
  <div class="flex flex-col gap-1">
    <span class="text-fg-muted text-2xs uppercase tracking-wider">Follow Mode</span>
    <div class="flex rounded-md overflow-hidden border border-stroke-section">
      <button
        class="flex-1 py-1.5 text-xs font-medium border-0 rounded-none transition-colors duration-100
          {rotFollow === 'off'
          ? 'bg-surface-input text-fg-bright hover:bg-surface-input'
          : 'bg-surface-app text-fg-secondary hover:bg-surface-section hover:text-fg-base'}"
        on:click={() => dispatch("follow", "off")}
      >Off</button>
      <button
        class="flex-1 py-1.5 text-xs font-medium border-0 border-l border-stroke-section rounded-none transition-colors duration-100
          {rotFollow === 'hf'
          ? 'bg-surface-input text-fg-bright hover:bg-surface-input'
          : 'bg-surface-app text-fg-secondary hover:bg-surface-section hover:text-fg-base'}"
        on:click={() => dispatch("follow", "hf")}
      >HF{#if hfAz !== null} <span class="text-accent-orange text-2xs">→{Number(hfAz).toFixed(0)}°</span>{/if}</button>
      <button
        class="flex-1 py-1.5 text-xs font-medium border-0 border-l border-stroke-section rounded-none transition-colors duration-100
          {rotFollow === 'sat'
          ? 'bg-surface-input text-fg-bright hover:bg-surface-input'
          : 'bg-surface-app text-fg-secondary hover:bg-surface-section hover:text-fg-base'}"
        on:click={() => dispatch("follow", "sat")}
      >SAT{#if satAz !== null} <span class="text-accent-orange text-2xs">↗{Number(satAz).toFixed(0)}°</span>{/if}</button>
    </div>
  </div>

  <!-- Park -->
  <div class="flex justify-end">
    <button
      class="text-xs py-1.5 px-4 text-fg-bright hover:text-fg-base"
      on:click={() => dispatch("park")}
    >Park ⟳</button>
  </div>

</div>
