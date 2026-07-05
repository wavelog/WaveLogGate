<script>
  import { createEventDispatcher } from "svelte";
  import TrxDisplay from "./status/TrxDisplay.svelte";
  import RotatorPanel from "./status/RotatorPanel.svelte";

  const dispatch = createEventDispatcher();

  export let freqMHz = "";
  export let mode = "";
  export let split = false;
  export let freqTxMHz = "";
  export let modeTx = "";
  export let statusMsg = "";
  export let qsoResult = null;
  export let radioEnabled = false;
  export let rotatorEnabled = false;
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
  export let queuePending = 0;

  // Two-step flush confirmation. window.confirm is unusable here: Wails'
  // WKWebView on macOS has no JS-dialog delegate, so confirm() silently
  // returns falsy and the button would never fire.
  let flushArmed = false;
  let flushArmTimer = null;

  function handleFlushClick() {
    if (!flushArmed) {
      flushArmed = true;
      clearTimeout(flushArmTimer);
      flushArmTimer = setTimeout(() => { flushArmed = false; }, 4000);
      return;
    }
    clearTimeout(flushArmTimer);
    flushArmed = false;
    dispatch("flush");
  }
</script>

<div class="py-2.5 px-3 flex flex-col gap-2">

  {#if statusMsg || qsoResult}
    <div class="px-1">
      {#if statusMsg}
        <div class="alert alert-info font-mono text-2xs">{statusMsg}</div>
      {/if}
      {#if qsoResult}
        {#if qsoResult.success}
          <div class="alert alert-success">
            ✓ QSO logged: <strong>{qsoResult.call}</strong>
            {qsoResult.band} {qsoResult.mode} {qsoResult.rstSent}/{qsoResult.rstRcvd} {qsoResult.timeOn}
          </div>
        {:else}
          <div class="alert alert-danger">
            ✗ QSO NOT logged: {qsoResult.reason || "unknown error"}
          </div>
        {/if}
      {/if}
    </div>
  {/if}

  {#if queuePending > 0}
    <div class="px-1 flex items-center gap-2">
      <div class="alert alert-danger flex-1 font-mono text-2xs">
        ⏳ {queuePending} QSO{queuePending === 1 ? "" : "s"} queued — will retry every 30s
      </div>
      <button
        class="text-2xs py-1 px-2 rounded-md border border-stroke-base text-fg-bright hover:bg-surface-input transition-colors duration-150"
        title="Retry sending queued QSOs now"
        on:click={() => dispatch("retry")}
      >Retry now</button>
      <button
        class="text-2xs py-1 px-2 rounded-md border transition-colors duration-150 {flushArmed
          ? 'border-red-500 text-red-400 hover:bg-red-500/10'
          : 'border-stroke-base text-fg-bright hover:bg-surface-input'}"
        title="Drop all buffered QSOs (cannot be undone)"
        on:click={handleFlushClick}
      >{flushArmed ? "Sure?" : "Flush"}</button>
    </div>
  {/if}

  {#if radioEnabled}
    <TrxDisplay {freqMHz} {mode} {split} {freqTxMHz} {modeTx} on:freqscroll on:txfreqscroll />
  {/if}
  
  {#if rotatorEnabled}
    <RotatorPanel
      {rotConnected} {rotMoving} {rotAz} {rotEl} {rotFollow} {hfAz} {satAz} {satEl}
      {demandedAz} {demandedEl}
      on:follow={(e) => dispatch("follow", e.detail)}
      on:park={() => dispatch("park")}
      on:rotscroll
      on:rotgoto
    />
  {/if}
</div>
