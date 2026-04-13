<script>
  import { onMount } from "svelte";
  import {
    GetConfig,
    SaveConfig,
    TestWavelog,
    GetStations,
  } from "../../wailsjs/go/main/App.js";
  import WavelogSection from "./config/WavelogSection.svelte";
  import RadioSection from "./config/RadioSection.svelte";
  import RotatorSection from "./config/RotatorSection.svelte";
  import ProfileModal from "./config/ProfileModal.svelte";
  import AdvancedModal from "./config/AdvancedModal.svelte";

  let cfg = null;
  let stations = [];
  let saveMsg = "";
  let testMsg = "";
  let testSuccess = null;
  let loading = true;

  let showProfileModal = false;
  let showAdvancedModal = false;

  onMount(async () => {
    cfg = await GetConfig();
    loading = false;
    if (cfg.profiles && cfg.profiles.length > 0) loadStations();
  });

  function activeProfile() {
    return { ...(cfg.profiles[cfg.profile] || cfg.profiles[0]) };
  }

  function setProfileField(key, value) {
    cfg.profiles[cfg.profile][key] = value;
    cfg = cfg; // trigger reactivity
  }

  async function loadStations() {
    const p = activeProfile();
    if (!p || !p.wavelog_url || !p.wavelog_key) return;
    stations = await GetStations(p.wavelog_url, p.wavelog_key);
    cfg = cfg; // force re-evaluation of selected= expressions after options populate
  }

  async function reloadConfig() {
    cfg = await GetConfig();
    stations = [];
    loadStations();
  }

  async function save() {
    cfg = await SaveConfig(cfg);
    saveMsg = "Saved ✓";
    setTimeout(() => (saveMsg = ""), 3000);
  }

  async function test() {
    testMsg = "Testing…";
    testSuccess = null;
    const result = await TestWavelog(activeProfile());
    testSuccess = result.success;
    testMsg = result.success ? "Connection OK ✓" : "Failed: " + result.reason;
    setTimeout(() => { testMsg = ""; testSuccess = null; }, 5000);
  }

  $: radioType = cfg
    ? cfg.profiles[cfg.profile]?.flrig_ena
      ? "flrig"
      : cfg.profiles[cfg.profile]?.hamlib_ena
        ? cfg.profiles[cfg.profile]?.hamlib_managed
          ? "internal"
          : "hamlib"
        : "none"
    : "none";

  $: rotatorEnabled = cfg?.profiles?.[cfg.profile]?.rotator_enabled ?? false;

  function setRadioType(type) {
    setProfileField("flrig_ena",     type === "flrig");
    setProfileField("hamlib_ena",    type === "hamlib" || type === "internal");
    setProfileField("hamlib_managed", type === "internal");
  }
</script>

{#if loading}
  <div class="p-5 text-fg-muted text-center">Loading…</div>
{:else if cfg}
  <div class="py-3 px-3 flex flex-col gap-2">

    <!-- Profile bar -->
    <div class="flex items-center justify-between bg-surface-card border border-stroke-section rounded-lg px-4 py-2.5">
      <div class="flex items-center gap-2.5">
        <span class="text-fg-bright text-2xs uppercase tracking-widest">Profile</span>
        <span class="text-accent-value text-sm font-semibold">
          {cfg.profileNames[cfg.profile] || "Profile " + (cfg.profile + 1)}
        </span>
      </div>
      <button
        class="text-2xs py-1 px-2.5 text-fg-bright hover:text-fg-base"
        on:click={() => (showProfileModal = true)}>Manage</button>
    </div>

    <!-- {#key cfg.profile} forces all inputs to be recreated when the active profile changes,
       preventing stale browser input state from leaking across profile switches. -->
    {#key cfg.profile}
      <WavelogSection
        profile={activeProfile()}
        {stations}
        on:fieldchange={(e) => setProfileField(e.detail.key, e.detail.value)}
        on:reloadstations={loadStations}
      />
      <RadioSection
        profile={activeProfile()}
        {radioType}
        on:fieldchange={(e) => setProfileField(e.detail.key, e.detail.value)}
        on:typechange={(e) => setRadioType(e.detail)}
      />
      <RotatorSection
        profile={activeProfile()}
        {rotatorEnabled}
        on:fieldchange={(e) => setProfileField(e.detail.key, e.detail.value)}
      />
    {/key}

    <!-- Bottom action bar -->
    <div class="border-t border-stroke-section pt-2.5 flex items-center justify-between">
      <div class="flex gap-2">
        <button class="border-stroke-accent text-fg-bright" on:click={save}>Save</button>
        <button on:click={test}>Test</button>
      </div>
      <div class="flex gap-1.5">
        <button on:click={() => (showAdvancedModal = true)}>⚙ Advanced</button>
      </div>
    </div>

    {#if saveMsg}
      <div class="alert alert-success py-1 px-3">{saveMsg}</div>
    {/if}
    {#if testMsg}
      <div
        class="alert py-1 px-3"
        class:alert-success={testSuccess}
        class:alert-danger={testSuccess === false}
        class:alert-info={testSuccess === null}
      >{testMsg}</div>
    {/if}

  </div>
{/if}

{#if showProfileModal && cfg}
  <ProfileModal
    {cfg}
    on:close={() => (showProfileModal = false)}
    on:configchanged={reloadConfig}
  />
{/if}

{#if showAdvancedModal}
  <AdvancedModal on:close={() => (showAdvancedModal = false)} />
{/if}
