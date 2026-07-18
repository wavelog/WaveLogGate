<script>
  import { createEventDispatcher } from "svelte";
  const dispatch = createEventDispatcher();

  export let profile;
  export let stations = [];

  // API key is masked by default; the eye button reveals it on demand.
  let showKey = false;

  // Wavelog v2 tokens (wl2_...) are scoped — without these the app silently loses
  // features, so spell out what to tick when generating the token.
  let showScopes = false;

  const scopes = [
    { name: "station:read", what: "load the station list" },
    { name: "qso:write", what: "upload QSOs" },
    { name: "radio:write", what: "send radio status" },
  ];
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
    <div class="relative flex-1">
      <input
        id="wl-key"
        type={showKey ? "text" : "password"}
        class="w-full pr-14"
        value={profile.wavelog_key}
        on:change={(e) => dispatch("fieldchange", { key: "wavelog_key", value: e.currentTarget.value })}
        on:blur={() => dispatch("reloadstations")}
      />
      <div class="absolute right-0 top-0 h-full flex items-center">
        <button
          type="button"
          tabindex="-1"
          class="h-full px-1.5 bg-transparent border-0 text-fg-label hover:text-fg-bright"
          title={showKey ? "Hide API key" : "Show API key"}
          aria-label={showKey ? "Hide API key" : "Show API key"}
          on:click={() => (showKey = !showKey)}
        >
          {#if showKey}
            <!-- eye with slash: key is currently visible -->
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" />
              <line x1="1" y1="1" x2="23" y2="23" />
            </svg>
          {:else}
            <!-- eye: key is currently masked -->
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
              <circle cx="12" cy="12" r="3" />
            </svg>
          {/if}
        </button>
        <button
          type="button"
          tabindex="-1"
          class="h-full pl-0.5 pr-2 bg-transparent border-0 text-fg-label hover:text-fg-bright"
          title="API token scopes"
          aria-label="API token scopes"
          aria-expanded={showScopes}
          on:click={() => (showScopes = !showScopes)}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" />
            <line x1="12" y1="16" x2="12" y2="12" />
            <line x1="12" y1="8" x2="12.01" y2="8" />
          </svg>
        </button>
      </div>
    </div>
  </div>

  {#if showScopes}
    <div class="mb-1.5 px-2 py-1.5 rounded bg-surface-card border border-stroke-subtle text-2xs text-fg-label leading-relaxed">
      <div class="mb-1">Wavelog v2 tokens (<code class="font-mono">wl2_…</code>) need these scopes:</div>
      {#each scopes as s}
        <div class="flex gap-2">
          <code class="w-24 flex-shrink-0 font-mono text-fg-bright">{s.name}</code>
          <span>{s.what}</span>
        </div>
      {/each}
      <div class="mt-1">Legacy v1 keys need no scopes.</div>
    </div>
  {/if}

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
