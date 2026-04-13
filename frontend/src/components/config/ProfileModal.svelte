<script>
  import { createEventDispatcher } from "svelte";
  import {
    CreateProfile,
    DeleteProfile,
    RenameProfile,
    SwitchProfile,
  } from "../../../wailsjs/go/main/App.js";

  const dispatch = createEventDispatcher();

  export let cfg;

  let newProfileName = "";
  let renameIndex = -1;
  let renameName = "";

  async function doCreateProfile() {
    if (!newProfileName.trim()) return;
    await CreateProfile(newProfileName.trim());
    newProfileName = "";
    dispatch("configchanged");
  }

  async function doDeleteProfile(i) {
    try {
      await DeleteProfile(i);
      dispatch("configchanged");
    } catch (e) {
      alert(e);
    }
  }

  async function doRenameProfile() {
    if (renameIndex < 0 || !renameName.trim()) return;
    await RenameProfile(renameIndex, renameName.trim());
    renameIndex = -1;
    renameName = "";
    dispatch("configchanged");
  }

  async function doSwitchProfile(i) {
    await SwitchProfile(i);
    dispatch("configchanged");
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
    <h4>Profiles</h4>

    <div class="flex flex-col gap-1 mb-2.5 max-h-48 overflow-y-auto">
      {#each cfg.profileNames as name, i}
        <div
          class="flex items-center justify-between px-2 py-1 bg-surface-section rounded border gap-1.5
            {i === cfg.profile ? 'border-stroke-accent' : 'border-stroke-section'}"
        >
          {#if renameIndex === i}
            <input
              type="text"
              bind:value={renameName}
              class="flex-1 bg-surface-input border border-stroke-base text-fg-base px-1.5 rounded text-xs"
            />
            <button on:click={doRenameProfile}>OK</button>
            <button on:click={() => (renameIndex = -1)}>✕</button>
          {:else}
            <span class="flex-1 text-xs">{name}</span>
            <div class="flex gap-1">
              {#if i !== cfg.profile}
                <button on:click={() => doSwitchProfile(i)}>Switch</button>
              {:else}
                <span class="text-xs text-accent-value px-1.5 py-px">Active</span>
              {/if}
              <button on:click={() => { renameIndex = i; renameName = name; }}>Rename</button>
              {#if cfg.profileNames.length > 2 && i !== cfg.profile}
                <button on:click={() => doDeleteProfile(i)}>Delete</button>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    </div>

    <div class="flex gap-1.5">
      <input
        type="text"
        bind:value={newProfileName}
        placeholder="New profile name"
        class="flex-1 bg-surface-input border border-stroke-base text-fg-base px-1.5 rounded text-xs"
      />
      <button on:click={doCreateProfile}>+ Add</button>
    </div>

    <div class="mt-2.5 text-right">
      <button on:click={() => dispatch("close")}>Close</button>
    </div>
  </div>
</div>
