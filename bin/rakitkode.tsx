#!/usr/bin/env bun
import React from "react";
import { render } from "ink";
import { RakitKode } from "../src/app.ts";
import { TUI } from "../src/tui/app.tsx";

const app = new RakitKode();
const emitter = app.getEmitter();

await app.bootstrap();

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
          if (result.action === "clear") {
            emitter.emit({ type: "done", data: {} });
            return;
          }
          if (result.output) {
            emitter.emit({
              type: "tool_call_output",
              data: { id: "cmd", name: "system", output: result.output, raw: result.output, error: false },
            });
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
      app.handleAllPatches("accept");
    }}
    onRejectDiff={(entryId) => {
      app.handlePatchAction(String(entryId), "reject");
    }}
    onRejectAllDiffs={() => {
      app.handleAllPatches("reject");
    }}
  />,
);

process.on("SIGINT", () => {
  app.abort();
  setTimeout(() => {
    instance.unmount();
    process.exit(0);
  }, 100);
});

process.on("SIGTERM", () => {
  app.abort();
  setTimeout(() => {
    instance.unmount();
    process.exit(0);
  }, 100);
});

const origLog = console.log;
const origError = console.error;
const origWarn = console.warn;
function silence() {
  console.log = () => {};
  console.error = () => {};
  console.warn = () => {};
}
if (process.env.NODE_ENV !== "debug") {
  process.on("exit", silence);
}
