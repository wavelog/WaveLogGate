<script>
  import { onMount, createEventDispatcher } from "svelte";
  import { GetUDPStatus, SaveAdvanced } from "../../../wailsjs/go/main/App.js";

  const dispatch = createEventDispatcher();

  let advUdpEnabled = true;
  let advUdpPort = 2333;
  let advMinimapEnabled = false;
  let advEmitEnabled = false;
  let advEmitPort = 2334;
  let advEmitHost = "127.0.0.1";
  let advStatus = "";

  $: portClash = advUdpEnabled && advEmitEnabled && advUdpPort === advEmitPort;

  onMount(async () => {
    const status = await GetUDPStatus();
    advUdpEnabled = status.enabled;
    advUdpPort = status.port;
    advMinimapEnabled = status.minimapEnabled;
    advEmitEnabled = status.emitEnabled;
    advEmitPort = status.emitPort;
    advEmitHost = status.emitHost;
  });

  async function save() {
    try {
      await SaveAdvanced(advUdpEnabled, advUdpPort, advMinimapEnabled, advEmitEnabled, advEmitPort, advEmitHost);
      advStatus = "Saved ✓";
      setTimeout(() => {
        advStatus = "";
        dispatch("close");
      }, 1500);
    } catch (e) {
      advStatus = "Error: " + e;
    }
  }
</script>

<div
  class="modal-overlay"
  on:click|self={() => dispatch("close")}
  on:keydown={(e) => e.key === "Escape" && dispatch("close")}
  role="dialog"
  aria-modal="true"
>
  <div class="modal">
    <h4>Advanced Settings</h4>

    <div class="flex items-center gap-1.5 mb-1">
      <label>
        <input type="checkbox" bind:checked={advUdpEnabled} />
        UDP Listener enabled
      </label>
    </div>

    <div class="flex items-center gap-1.5 mb-1">
      <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs justify-end" for="adv-port">UDP Port</label>
      <input
        id="adv-port"
        type="number"
        class="flex-none w-field-xs"
        bind:value={advUdpPort}
        min="1024"
        max="65535"
        disabled={!advUdpEnabled}
      />
    </div>

    <div class="flex items-center gap-1.5 mb-1 mt-2 border-t border-stroke-section pt-2">
      <label>
        <input type="checkbox" bind:checked={advEmitEnabled} />
        UDP QSO Emit enabled
      </label>
    </div>

    <div class="flex items-center gap-1.5 mb-1">
      <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs justify-end" for="adv-emit-host">Emit Host</label>
      <input
        id="adv-emit-host"
        type="text"
        class="flex-1"
        bind:value={advEmitHost}
        disabled={!advEmitEnabled}
      />
    </div>

    <div class="flex items-center gap-1.5 mb-1">
      <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs justify-end" for="adv-emit-port">Emit Port</label>
      <input
        id="adv-emit-port"
        type="number"
        class="flex-none w-field-xs"
        bind:value={advEmitPort}
        min="1024"
        max="65535"
        disabled={!advEmitEnabled}
      />
    </div>

    {#if portClash}
      <div class="alert alert-danger mt-1">Emit port must differ from the listener port ({advUdpPort})</div>
    {/if}

    <div class="flex items-center gap-1.5 mb-1 mt-2 border-t border-stroke-section pt-2">
      <label>
        <input type="checkbox" bind:checked={advMinimapEnabled} />
        Show polar map in Mini-Mode
      </label>
    </div>

    {#if advStatus}
      <div class="alert alert-info mt-2">{advStatus}</div>
    {/if}

    <div class="mt-3 flex gap-1.5 justify-end">
      <button on:click={save}>Save</button>
      <button on:click={() => dispatch("close")}>Cancel</button>
    </div>
  </div>
</div>
