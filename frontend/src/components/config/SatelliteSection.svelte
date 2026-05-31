<script>
  import { createEventDispatcher } from "svelte";
  const dispatch = createEventDispatcher();

  export let profile;
  export let satEnabled = false;
</script>

<section class="bg-surface-section border border-stroke-section rounded-lg px-4 py-3">
  <div class="flex items-center justify-between {satEnabled ? 'mb-3' : ''}">
    <div class="text-2xs text-fg-bright font-semibold uppercase tracking-wider pl-2 border-l-2 border-cyan-400">
      Satellite / Transverter
    </div>
    <label class="text-fg-label text-xs">
      <input
        type="checkbox"
        checked={satEnabled}
        on:change={(e) => dispatch("fieldchange", { key: "sat_enabled", value: e.currentTarget.checked })}
      />
      Enable
    </label>
  </div>

  {#if satEnabled}
    <div class="flex flex-col gap-1.5">
      <div class="flex items-center gap-2">
        <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="sat-tx-offset">TX Offset</label>
        <input
          id="sat-tx-offset"
          type="number"
          class="flex-none w-field-sm"
          value={profile.sat_tx_offset_mhz || 0}
          on:change={(e) => dispatch("fieldchange", { key: "sat_tx_offset_mhz", value: Number(e.currentTarget.value) })}
          min="0" step="0.001"
          placeholder="0"
        />
        <span class="text-fg-muted text-2xs">MHz</span>
      </div>
      <div class="flex items-center gap-2">
        <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="sat-rx-offset">RX Offset</label>
        <input
          id="sat-rx-offset"
          type="number"
          class="flex-none w-field-sm"
          value={profile.sat_rx_offset_mhz || 0}
          on:change={(e) => dispatch("fieldchange", { key: "sat_rx_offset_mhz", value: Number(e.currentTarget.value) })}
          min="0" step="0.001"
          placeholder="0"
        />
        <span class="text-fg-muted text-2xs">MHz</span>
      </div>
      <div class="flex items-center gap-2">
        <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="sat-name">Satellite</label>
        <input
          id="sat-name"
          type="text"
          class="flex-none w-field-sm"
          value={profile.sat_name || ""}
          on:change={(e) => dispatch("fieldchange", { key: "sat_name", value: e.currentTarget.value })}
          placeholder="QO-100"
        />
      </div>
      <div class="flex items-center gap-2">
        <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="sat-mode">Sat Mode</label>
        <input
          id="sat-mode"
          type="text"
          class="flex-none w-field-sm"
          value={profile.sat_mode || ""}
          on:change={(e) => dispatch("fieldchange", { key: "sat_mode", value: e.currentTarget.value })}
          placeholder="S/X"
        />
      </div>
    </div>
  {/if}
</section>
