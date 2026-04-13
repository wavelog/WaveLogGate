<script>
  import { createEventDispatcher } from "svelte";
  const dispatch = createEventDispatcher();

  export let profile;
  export let stations = [];
</script>

<section class="bg-surface-section border border-stroke-section rounded-lg px-4 py-3">
  <div class="text-2xs text-fg-bright font-semibold uppercase tracking-wider mb-3 pl-2 border-l-2 border-stroke-accent">
    Wavelog
  </div>

  <div class="flex items-center gap-2 mb-1.5">
    <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="wl-url">URL</label>
    <input
      id="wl-url"
      type="text"
      class="flex-1 w-full"
      value={profile.wavelog_url}
      on:change={(e) => dispatch("fieldchange", { key: "wavelog_url", value: e.currentTarget.value })}
      on:blur={() => dispatch("reloadstations")}
      placeholder="https://log.example.com/index.php"
    />
  </div>

  <div class="flex items-center gap-2 mb-1.5">
    <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="wl-key">API Key</label>
    <input
      id="wl-key"
      type="text"
      class="flex-1 w-full"
      value={profile.wavelog_key}
      on:change={(e) => dispatch("fieldchange", { key: "wavelog_key", value: e.currentTarget.value })}
      on:blur={() => dispatch("reloadstations")}
    />
  </div>

  <div class="flex items-center gap-2 mb-1.5">
    <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="wl-station">Station</label>
    <select
      id="wl-station"
      class="flex-1 w-full"
      on:change={(e) => dispatch("fieldchange", { key: "wavelog_id", value: e.currentTarget.value })}
    >
      <option value="0" selected={profile.wavelog_id === "0" || profile.wavelog_id === 0}>— select —</option>
      {#each stations as s}
        <option value={s.station_id} selected={String(s.station_id) === String(profile.wavelog_id)}>
          {s.station_callsign} ({s.station_profile_name})
        </option>
      {/each}
    </select>
    <button class="flex-shrink-0 py-1 px-2 text-sm" on:click={() => dispatch("reloadstations")} title="Reload stations">↻</button>
  </div>

  <div class="flex items-center gap-2">
    <label class="w-field-xs flex-shrink-0 text-fg-label text-2xs" for="wl-radio">Radio name</label>
    <input
      id="wl-radio"
      type="text"
      class="flex-none w-field-sm"
      value={profile.wavelog_radioname}
      on:change={(e) => dispatch("fieldchange", { key: "wavelog_radioname", value: e.currentTarget.value })}
    />
  </div>
</section>
