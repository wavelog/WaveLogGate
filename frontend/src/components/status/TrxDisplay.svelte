<script>
  import { createEventDispatcher } from "svelte";
  export let freqMHz = "";
  export let mode = "";
  export let split = false;
  export let freqTxMHz = "";
  export let modeTx = "";

  const dispatch = createEventDispatcher();

  function buildSpans(freq) {
    if (!freq) return [];
    const dotIdx = freq.indexOf(".");
    return freq.split("").map((ch, i) => {
      if (ch === ".") return { char: ch, stepHz: 0 };
      let stepHz;
      if (i < dotIdx) {
        const rightPos = dotIdx - 1 - i;
        stepHz = Math.pow(10, rightPos) * 1_000_000;
      } else {
        const decPos = i - dotIdx - 1;
        stepHz = Math.round(Math.pow(10, -(decPos + 1)) * 1_000_000);
      }
      return { char: ch, stepHz };
    });
  }

  function onWheel(e, stepHz, tx = false) {
    if (!stepHz) return;
    const delta = e.deltaY < 0 ? 1 : -1;
    dispatch(tx ? "txfreqscroll" : "freqscroll", { deltaHz: delta * stepHz });
  }

  $: rxSpans = buildSpans(freqMHz);
  $: txSpans = buildSpans(freqTxMHz);
</script>

<div class="bg-surface-card border border-stroke-subtle rounded-lg px-4 py-3 font-mono">
  <div class="text-fg-muted text-2xs uppercase tracking-widest mb-2">Transceiver</div>
  {#if freqMHz}
    {#if split}
      <div class="flex items-start justify-between gap-4">
        <div class="flex items-start gap-4">
          <div>
            <div class="text-fg-muted text-2xs uppercase tracking-wider mb-1">RX</div>
            <div class="text-accent-value text-2xl font-bold tracking-tight leading-none">
              {#each rxSpans as span}
                {#if span.stepHz}
                  <span style="cursor:ns-resize" on:wheel|preventDefault={(e) => onWheel(e, span.stepHz)}>{span.char}</span>
                {:else}
                  {span.char}
                {/if}
              {/each}
            </div>
            <div class="text-fg-muted text-xs mt-1.5">MHz</div>
          </div>
          {#if freqTxMHz}
            <div class="border-l border-stroke-section pl-4">
              <div class="text-fg-muted text-2xs uppercase tracking-wider mb-1">TX</div>
              <div class="text-accent-value text-2xl font-bold tracking-tight leading-none">
                {#each txSpans as span}
                  {#if span.stepHz}
                    <span style="cursor:ns-resize" on:wheel|preventDefault={(e) => onWheel(e, span.stepHz, true)}>{span.char}</span>
                  {:else}
                    {span.char}
                  {/if}
                {/each}
              </div>
              <div class="text-fg-muted text-xs mt-1.5">MHz</div>
            </div>
          {/if}
        </div>
        <div class="flex flex-col items-end gap-1.5">
          <span class="bg-accent-orange/10 border border-accent-orange/40 text-accent-orange text-2xs font-bold px-2 py-0.5 rounded-md tracking-wider">SPLIT</span>
          {#if mode}
            <div class="bg-surface-app border border-stroke-section text-accent-orange text-l font-semibold px-2.5 py-1 rounded-md">{mode}</div>
          {/if}
          {#if modeTx && modeTx !== mode}
            <div class="bg-surface-app border border-stroke-section text-accent-orange text-l font-semibold px-2.5 py-1 rounded-md">{modeTx}</div>
          {/if}
        </div>
      </div>
    {:else}
      <div class="flex items-start justify-between">
        <div>
          <div class="text-accent-value text-2xl font-bold tracking-tight leading-none">
            {#each rxSpans as span}
              {#if span.stepHz}
                <span style="cursor:ns-resize" on:wheel|preventDefault={(e) => onWheel(e, span.stepHz)}>{span.char}</span>
              {:else}
                {span.char}
              {/if}
            {/each}
          </div>
          <div class="text-fg-muted text-xs mt-1.5">MHz</div>
        </div>
        {#if mode}
          <div class="flex flex-col items-end gap-1.5 mt-5">
            <div class="bg-surface-app border border-stroke-section text-accent-orange text-l font-semibold px-2.5 py-1 rounded-md">{mode}</div>
          </div>
        {/if}
      </div>
    {/if}
  {:else}
    <div class="text-fg-dim text-sm italic mt-2">No radio data</div>
  {/if}
</div>
