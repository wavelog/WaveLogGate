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
</script>

<div class="py-2.5 px-3 flex flex-col gap-2">

  <section class="bg-surface-card border border-stroke-subtle rounded-lg px-4 py-3">
    <div class="text-fg-muted text-2xs uppercase tracking-widest font-semibold mb-3">
      Status
    </div>
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
    {:else}
      <div class="text-fg-dim text-sm italic">-</div>
    {/if}
  </section>

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
