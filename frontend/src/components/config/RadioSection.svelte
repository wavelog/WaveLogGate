<script>
  import { createEventDispatcher, onMount, onDestroy } from "svelte";
  import {
    GetHamlibStatus,
    DownloadHamlib,
    SearchRadioModels,
    GetSerialPorts,
    StartHamlib,
    StopHamlib,
  } from "../../../wailsjs/go/main/App.js";
  import { EventsOn, EventsOff } from "../../../wailsjs/runtime/runtime.js";

  const dispatch = createEventDispatcher();

  export let profile;
  export let radioType = "none";

  // Track last active type so re-enabling restores the previous selection.
  let lastType = radioType !== "none" ? radioType : "flrig";
  $: if (radioType !== "none") lastType = radioType;
  $: radioEnabled = radioType !== "none";

  // ── Hamlib managed state ────────────────────────────────────────────────────
  let hamlibStatus = { installed: false, version: "", running: false, statusMsg: "", installGuide: "", canDownload: false };
  let downloadProgress = -1; // -1 = idle, 0-100 = in progress
  let downloadMsg = "";
  let serialPorts = [];
  let modelQuery = "";
  let modelResults = [];
  let modelDropdownOpen = false;
  let showAdvancedSerial = false;
  let startStopBusy = false;
  let selectedModelLabel = "";
  let previousModelLabel = ""; // Track previous model for Escape key
  let previousModelId = null;   // Track previous model ID for restoration

  // "internal" radio type means managed rigctld.
  $: hamlibManaged = radioType === "internal";

  // Load hamlib status/ports whenever managed mode is active.
  let hamlibDataLoaded = false;
  $: if (hamlibManaged && !hamlibDataLoaded) {
    hamlibDataLoaded = true;
    loadHamlibData();
  }
  $: if (!hamlibManaged) hamlibDataLoaded = false; // reset so next open re-fetches

  async function loadHamlibData() {
    hamlibStatus = await GetHamlibStatus();
    serialPorts = await GetSerialPorts();
    if (profile.hamlib_model) {
      const all = await SearchRadioModels("");
      const match = all.find((m) => m.id === profile.hamlib_model);
      if (match) {
        selectedModelLabel = match.label;
        previousModelLabel = selectedModelLabel;
        previousModelId = match.id;
      }
    }
  }

  onMount(() => {
    EventsOn("hamlib:status", onHamlibStatus);
    EventsOn("hamlib:download_progress", onDownloadProgress);
  });

  onDestroy(() => {
    EventsOff("hamlib:status");
    EventsOff("hamlib:download_progress");
  });

  function onHamlibStatus(data) {
    hamlibStatus = { ...hamlibStatus, running: data.running, statusMsg: data.message || (data.running ? "Running" : "Stopped") };
  }

  function onDownloadProgress(data) {
    downloadProgress = data.percent;
  }

  function onEnableChange(e) {
    dispatch("typechange", e.currentTarget.checked ? lastType : "none");
  }

  function onTypeChange(e) {
    dispatch("typechange", e.currentTarget.value);
  }

  async function searchModels(q) {
    modelResults = await SearchRadioModels(q);
    modelDropdownOpen = modelResults.length > 0;
  }

  function onModelInput(e) {
    modelQuery = e.currentTarget.value;
    selectedModelLabel = modelQuery;
    searchModels(modelQuery);
  }

  function selectModel(m) {
    // Store previous model before selecting new one
    if (previousModelId !== null) {
      previousModelLabel = selectedModelLabel;
    }

    selectedModelLabel = m.label;
    previousModelId = m.id;
    modelQuery = "";
    modelDropdownOpen = false;
    dispatch("fieldchange", { key: "hamlib_model", value: m.id });
  }

  function onModelKeydown(e) {
    if (e.key === "Escape") {
      // Restore previous model when Escape is pressed
      if (previousModelLabel) {
        selectedModelLabel = previousModelLabel;
        modelQuery = "";
        modelDropdownOpen = false;
        // Also restore the actual model value
        if (previousModelId !== null) {
          dispatch("fieldchange", { key: "hamlib_model", value: previousModelId });
        }
      }
    }
  }

  async function startManaged() {
    startStopBusy = true;
    try {
      await StartHamlib();
    } finally {
      startStopBusy = false;
      hamlibStatus = await GetHamlibStatus();
    }
  }

  async function stopManaged() {
    startStopBusy = true;
    try {
      await StopHamlib();
    } finally {
      startStopBusy = false;
      hamlibStatus = await GetHamlibStatus();
    }
  }

  async function triggerDownload() {
    downloadMsg = "";
    downloadProgress = 0;
    const result = await DownloadHamlib();
    downloadProgress = -1;
    if (!result.success) {
      downloadMsg = result.message;
      return;
    }
    downloadMsg = "Download complete — rigctld ready.";
    // Force a full re-fetch so the installed state is picked up reliably.
    hamlibDataLoaded = false;
    await loadHamlibData();
  }

  async function detectInstall() {
    hamlibStatus = await GetHamlibStatus();
    downloadMsg = hamlibStatus.installed
      ? "rigctld detected: " + (hamlibStatus.version || "unknown version")
      : "rigctld not found in PATH. " + hamlibStatus.installGuide;
  }

  const BAUD_RATES = [1200, 4800, 9600, 19200, 38400, 57600, 115200];
</script>

<section class="bg-surface-section border border-stroke-section rounded-lg px-4 py-3">
  <div class="flex items-center justify-between {radioEnabled ? 'mb-3' : ''}">
    <div class="text-2xs text-fg-bright font-semibold uppercase tracking-wider pl-2 border-l-2 border-stroke-accent">
      Radio Control
    </div>
    <label class="text-fg-label text-xs">
      <input
        type="checkbox"
        checked={radioEnabled}
        on:change={onEnableChange}
      />
      Enable
    </label>
  </div>

  {#if radioEnabled}
    <div class="flex flex-col gap-1.5">
      <!-- Type selector -->
      <div class="flex items-center gap-2">
        <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="radio-type">Type</label>
        <select
          id="radio-type"
          class="flex-none w-field-sm"
          value={radioType}
          on:change={onTypeChange}
        >
          <option value="flrig">FLRig</option>
          <option value="hamlib">Hamlib</option>
          <option value="internal">Internal</option>
        </select>
      </div>

      <!-- Host / Port — hidden for "internal" (rigctld is local; port is in the managed section) -->
      {#if radioType !== "internal"}
        <div class="flex items-center gap-2">
          <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="radio-host">Host</label>
          <input
            id="radio-host"
            type="text"
            class="flex-none w-field-sm"
            value={radioType === "flrig" ? profile.flrig_host : profile.hamlib_host}
            on:change={(e) => dispatch("fieldchange", {
              key: radioType === "flrig" ? "flrig_host" : "hamlib_host",
              value: e.currentTarget.value,
            })}
          />
          <label class="text-fg-label text-2xs ml-1 cursor-default" for="radio-port">Port</label>
          <input
            id="radio-port"
            type="text"
            class="flex-none w-field-xs"
            value={radioType === "flrig" ? profile.flrig_port : profile.hamlib_port}
            on:change={(e) => dispatch("fieldchange", {
              key: radioType === "flrig" ? "flrig_port" : "hamlib_port",
              value: e.currentTarget.value,
            })}
          />
        </div>
      {/if}

      <!-- Checkboxes row -->
      <div class="flex items-center gap-5 flex-wrap">
        <label class="text-fg-label text-xs">
          <input
            type="checkbox"
            checked={profile.wavelog_pmode}
            on:change={(e) => dispatch("fieldchange", { key: "wavelog_pmode", value: e.currentTarget.checked })}
          />
          Set MODE on QSY
        </label>
        {#if radioType === "hamlib" || radioType === "internal"}
          <label class="text-fg-label text-xs">
            <input
              type="checkbox"
              checked={profile.ignore_pwr}
              on:change={(e) => dispatch("fieldchange", { key: "ignore_pwr", value: e.currentTarget.checked })}
            />
            Ignore Power
          </label>
        {/if}
      </div>

      <!-- ── Internal (managed rigctld) section ────────────────────────────── -->
      {#if hamlibManaged}
        <div class="mt-1 flex flex-col gap-2 border-t border-stroke-section pt-2">

          <!-- Status bar -->
          <div class="flex items-center justify-between gap-2 text-2xs">
            <span class="text-fg-muted">
              {hamlibStatus.installed
                ? (hamlibStatus.version ? "rigctld " + hamlibStatus.version : "rigctld installed")
                : "rigctld not found"}
            </span>
            <span
              class="font-medium"
              class:text-green-400={hamlibStatus.running}
              class:text-red-400={!hamlibStatus.running && hamlibStatus.statusMsg?.startsWith("Error")}
              class:text-fg-muted={!hamlibStatus.running && !hamlibStatus.statusMsg?.startsWith("Error")}
            >
              {hamlibStatus.statusMsg || (hamlibStatus.running ? "Running" : "Stopped")}
            </span>
            <div class="flex gap-1">
              <button
                class="text-2xs py-0.5 px-2"
                disabled={startStopBusy || hamlibStatus.running}
                on:click={startManaged}
              >Start</button>
              <button
                class="text-2xs py-0.5 px-2"
                disabled={startStopBusy || !hamlibStatus.running}
                on:click={stopManaged}
              >Stop</button>
            </div>
          </div>

          <!-- Download / install section -->
          {#if !hamlibStatus.installed}
            <div class="bg-surface-card border border-stroke-section rounded p-2 flex flex-col gap-1.5">
              <div class="text-2xs text-fg-bright font-semibold">Install rigctld</div>
              <div class="text-2xs text-fg-muted whitespace-pre-wrap">{hamlibStatus.installGuide}</div>
              <div class="flex gap-2 flex-wrap">
                <!-- Windows only: automatic download -->
                {#if hamlibStatus.canDownload}
                  <button class="text-2xs py-0.5 px-2 border-stroke-accent text-fg-bright" on:click={triggerDownload}
                    disabled={downloadProgress >= 0}>
                    {downloadProgress >= 0 ? `Downloading… ${downloadProgress}%` : "Download rigctld"}
                  </button>
                {/if}
                <button class="text-2xs py-0.5 px-2" on:click={detectInstall}>Detect</button>
              </div>
              {#if downloadProgress >= 0}
                <div class="w-full bg-surface-section rounded h-1.5">
                  <div class="h-1.5 rounded bg-accent-value transition-all" style="width:{downloadProgress}%"></div>
                </div>
              {/if}
              {#if downloadMsg}
                <div class="text-2xs"
                  class:text-green-400={downloadMsg.startsWith("Download complete") || downloadMsg.startsWith("rigctld detected")}
                  class:text-red-400={!downloadMsg.startsWith("Download complete") && !downloadMsg.startsWith("rigctld detected")}
                >{downloadMsg}</div>
              {/if}
            </div>
          {:else}
            <!-- Already installed: compact detect button -->
            <div class="flex items-center gap-2">
              <button class="text-2xs py-0.5 px-2" on:click={detectInstall}>Re-detect</button>
              {#if downloadMsg}
                <span class="text-2xs text-fg-muted">{downloadMsg}</span>
              {/if}
            </div>
          {/if}

          <!-- Radio model search -->
          <div class="flex flex-col gap-1 relative">
            <label class="text-fg-label text-2xs" for="hamlib-model">Radio Model</label>
            <input
              id="hamlib-model"
              type="text"
              class="w-full"
              placeholder="Search manufacturer or model…"
              value={selectedModelLabel}
              on:input={onModelInput}
              on:focus={(e) => { modelQuery = ""; searchModels(""); }}
              on:blur={() => setTimeout(() => { modelDropdownOpen = false; }, 150)}
              on:keydown={onModelKeydown}
              autocomplete="off"
            />
            {#if modelDropdownOpen}
              <div class="absolute top-full left-0 right-0 z-50 bg-surface-card border border-stroke-section rounded shadow-lg max-h-48 overflow-y-auto text-xs">
                {#each modelResults.slice(0, 50) as m}
                  <button
                    class="w-full text-left px-2 py-1 hover:bg-surface-section"
                    on:mousedown={() => selectModel(m)}
                  >
                    <span class="text-fg-bright">{m.label}</span>
                  </button>
                {/each}
                {#if modelResults.length > 50}
                  <div class="px-2 py-1 text-fg-muted">…{modelResults.length - 50} more, refine your search</div>
                {/if}
              </div>
            {/if}
          </div>

          <!-- Serial port -->
          <div class="flex items-center gap-2">
            <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="hamlib-device">Port</label>
            {#if serialPorts.length > 0}
              <select
                id="hamlib-device"
                class="flex-1"
                value={profile.hamlib_device || ""}
                on:change={(e) => dispatch("fieldchange", { key: "hamlib_device", value: e.currentTarget.value })}
              >
                <option value="">— select port —</option>
                {#each serialPorts as p}
                  <option value={p}>{p}</option>
                {/each}
              </select>
            {:else}
              <input
                id="hamlib-device"
                type="text"
                class="flex-1"
                value={profile.hamlib_device || ""}
                placeholder="e.g. /dev/ttyUSB0 or COM3"
                on:change={(e) => dispatch("fieldchange", { key: "hamlib_device", value: e.currentTarget.value })}
              />
            {/if}
          </div>

          <!-- Baud rate -->
          <div class="flex items-center gap-2">
            <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="hamlib-baud">Baud</label>
            <select
              id="hamlib-baud"
              class="flex-none w-field-sm"
              value={profile.hamlib_baud || 9600}
              on:change={(e) => dispatch("fieldchange", { key: "hamlib_baud", value: Number(e.currentTarget.value) })}
            >
              {#each BAUD_RATES as rate}
                <option value={rate}>{rate}</option>
              {/each}
            </select>
          </div>

          <!-- Advanced serial settings (collapsible) -->
          <button
            class="text-2xs text-fg-muted text-left py-0.5"
            on:click={() => (showAdvancedSerial = !showAdvancedSerial)}
          >
            {showAdvancedSerial ? "▾" : "▸"} Advanced serial settings
          </button>
          {#if showAdvancedSerial}
            <div class="flex flex-col gap-1.5 pl-3 border-l border-stroke-section">
              <!-- Parity -->
              <div class="flex items-center gap-2">
                <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="hamlib-parity">Parity</label>
                <select
                  id="hamlib-parity"
                  class="flex-none w-field-sm"
                  value={profile.hamlib_parity || "none"}
                  on:change={(e) => dispatch("fieldchange", { key: "hamlib_parity", value: e.currentTarget.value })}
                >
                  <option value="none">None</option>
                  <option value="odd">Odd</option>
                  <option value="even">Even</option>
                </select>
              </div>
              <!-- Stop bits -->
              <div class="flex items-center gap-2">
                <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="hamlib-stopbits">Stop bits</label>
                <select
                  id="hamlib-stopbits"
                  class="flex-none w-field-sm"
                  value={profile.hamlib_stop_bits || 1}
                  on:change={(e) => dispatch("fieldchange", { key: "hamlib_stop_bits", value: Number(e.currentTarget.value) })}
                >
                  <option value={1}>1</option>
                  <option value={2}>2</option>
                </select>
              </div>
              <!-- Handshake -->
              <div class="flex items-center gap-2">
                <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="hamlib-handshake">Handshake</label>
                <select
                  id="hamlib-handshake"
                  class="flex-none w-field-sm"
                  value={profile.hamlib_handshake || "none"}
                  on:change={(e) => dispatch("fieldchange", { key: "hamlib_handshake", value: e.currentTarget.value })}
                >
                  <option value="none">None</option>
                  <option value="rtscts">RTS/CTS</option>
                  <option value="xonxoff">XON/XOFF</option>
                </select>
              </div>
            </div>
          {/if}
        </div>
      {/if}
      <!-- ── end managed rigctld section ─────────────────────────────────── -->
    </div>
  {/if}
</section>
