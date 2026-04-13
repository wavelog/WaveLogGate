<script>
  import { createEventDispatcher } from "svelte";
  const dispatch = createEventDispatcher();

  export let profile;
  export let rotatorEnabled = false;
</script>

<section class="bg-surface-section border border-stroke-section rounded-lg px-4 py-3">
  <div class="flex items-center justify-between {rotatorEnabled ? 'mb-3' : ''}">
    <div class="text-2xs text-fg-bright font-semibold uppercase tracking-wider pl-2 border-l-2 border-stroke-accent">
      Rotator Control
    </div>
    <label class="text-fg-label text-xs">
      <input
        type="checkbox"
        checked={rotatorEnabled}
        on:change={(e) => dispatch("fieldchange", { key: "rotator_enabled", value: e.currentTarget.checked })}
      />
      Enable
    </label>
  </div>

  {#if rotatorEnabled}
    <div class="flex flex-col gap-1.5">
      <div class="flex items-center gap-2">
        <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="rot-host">Host</label>
        <input
          id="rot-host"
          type="text"
          class="flex-none w-field-sm"
          value={profile.rotator_host}
          on:change={(e) => dispatch("fieldchange", { key: "rotator_host", value: e.currentTarget.value })}
          placeholder="127.0.0.1"
        />
        <label class="text-fg-label text-2xs ml-1 cursor-default" for="rot-port">Port</label>
        <input
          id="rot-port"
          type="text"
          class="flex-none w-field-xs"
          value={profile.rotator_port}
          on:change={(e) => dispatch("fieldchange", { key: "rotator_port", value: e.currentTarget.value })}
        />
      </div>
      <div class="flex items-center gap-2">
        <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="rot-threshold-az">Threshold</label>
        <input
          id="rot-threshold-az"
          type="number"
          class="flex-none w-field-xs"
          value={profile.rotator_threshold_az}
          on:change={(e) => dispatch("fieldchange", { key: "rotator_threshold_az", value: Number(e.currentTarget.value) })}
          min="0" max="360" step="0.5"
        />
        <span class="text-fg-muted text-2xs">° Az</span>
        <input
          id="rot-threshold-el"
          type="number"
          class="flex-none w-field-xs"
          value={profile.rotator_threshold_el}
          on:change={(e) => dispatch("fieldchange", { key: "rotator_threshold_el", value: Number(e.currentTarget.value) })}
          min="0" max="90" step="0.5"
        />
        <span class="text-fg-muted text-2xs">° El</span>
      </div>
      <div class="flex items-center gap-2">
        <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="rot-park-az">Park</label>
        <input
          id="rot-park-az"
          type="number"
          class="flex-none w-field-xs"
          value={profile.rotator_park_az}
          on:change={(e) => dispatch("fieldchange", { key: "rotator_park_az", value: Number(e.currentTarget.value) })}
          min="0" max="360" step="1"
        />
        <span class="text-fg-muted text-2xs">° Az</span>
        <input
          id="rot-park-el"
          type="number"
          class="flex-none w-field-xs"
          value={profile.rotator_park_el}
          on:change={(e) => dispatch("fieldchange", { key: "rotator_park_el", value: Number(e.currentTarget.value) })}
          min="0" max="90" step="1"
        />
        <span class="text-fg-muted text-2xs">° El</span>
      </div>
    </div>
  {/if}
</section>
