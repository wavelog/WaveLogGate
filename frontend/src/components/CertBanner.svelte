<script>
  import { InstallCert } from "../../wailsjs/go/main/App.js";

  export let certInfo = null; // cert.Info from backend

  let dismissed = false;
  let installing = false;
  let result = null; // cert.InstallResult

  async function install() {
    installing = true;
    result = null;
    try {
      result = await InstallCert();
    } finally {
      installing = false;
    }
  }

  function dismiss() {
    dismissed = true;
  }
</script>

{#if certInfo && !dismissed}
  <div class="bg-surface-section border-b border-stroke-subtle px-3 py-2 flex flex-col gap-1.5">
    <div class="flex items-start justify-between gap-2">
      <div class="flex items-center gap-2">
        <i class="fa-solid fa-shield-halved text-accent-orange text-xs flex-shrink-0"></i>
        <span class="text-fg-base text-2xs">
          HTTPS/WSS require a trusted certificate.
        </span>
      </div>
      <button
        class="border-0 bg-transparent text-fg-dim hover:text-fg-base p-0 flex-shrink-0 leading-none"
        title="Dismiss"
        on:click={dismiss}
      >
        <i class="fa-solid fa-xmark text-xs"></i>
      </button>
    </div>

    {#if result}
      {#if result.success}
        <div class="alert alert-success">
          ✓ {result.message}
        </div>
      {:else}
        <div class="alert alert-danger">
          ✗ {result.message}
        </div>
        {#if result.command}
          <div class="font-mono text-2xs text-fg-muted bg-surface-app rounded px-2 py-1 break-all select-all">
            {result.command}
          </div>
        {/if}
      {/if}
    {:else}
      <button
        class="self-start text-2xs py-1 px-3"
        disabled={installing}
        on:click={install}
      >
        {#if installing}
          <i class="fa-solid fa-spinner fa-spin mr-1"></i>Installing…
        {:else}
          <i class="fa-solid fa-certificate mr-1"></i>Install Certificate
        {/if}
      </button>
    {/if}
  </div>
{/if}
