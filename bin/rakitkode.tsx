#!/usr/bin/env bun
import React from "react";
import { render } from "ink";
import { RakitKode } from "../src/app.ts";
import { TUI } from "../src/tui/app.tsx";

const app = new RakitKode();
const emitter = app.getEmitter();

let currentAbortController: AbortController | null = null;

const instance = render(
  <TUI
    emitter={emitter}
    onSubmit={async (input: string) => {
      if (input.startsWith("/")) {
        const result = await app.handleCommand(input);
        if (result) {
          if (result.action === "exit") {
            app.shutdown();
            instance.unmount();
            process.exit(0);
          }
        }
        emitter.emit({ type: "done", data: {} });
        return;
      }
      await app.processInput(input);
    }}
    onAbort={() => {
      app.abort();
    }}
    onExit={() => {
      app.shutdown();
      instance.unmount();
      process.exit(0);
    }}
    modelName={app.getModelName()}
    providerName={app.getProviderName()}
    onAcceptDiff={(entryId) => {
      app.handlePatchAction(String(entryId), "accept");
    }}
    onAcceptAllDiffs={() => {
      app.getPatchManager().acceptAll();
    }}
    onRejectDiff={(entryId) => {
      app.handlePatchAction(String(entryId), "reject");
    }}
  />,
);

process.on("SIGINT", () => {
  app.abort();
});

process.on("SIGTERM", () => {
  app.shutdown();
  instance.unmount();
  process.exit(0);
});
