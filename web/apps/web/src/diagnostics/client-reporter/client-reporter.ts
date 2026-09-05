import { ingestClientDiagnostics } from "@loomarr/api/endpoints/diagnostics";
import type { ClientObservation as GeneratedClientObservation } from "@loomarr/api/models/clientObservation";
import { observeApiFailures } from "@loomarr/api/mutator";
import {
  ClientDiagnosticsReporter,
  type ClientObservation,
  type SendBatch,
} from "@loomarr/core/client-diagnostics";

type WebPlatform = "chromium" | "firefox" | "webkit" | "unknown_web";

const webPlatform = (): WebPlatform => {
  const ua = navigator.userAgent.toLowerCase();
  if (ua.includes("firefox")) return "firefox";
  if (ua.includes("applewebkit") && !ua.includes("chrome") && !ua.includes("chromium")) return "webkit";
  if (ua.includes("chrome") || ua.includes("chromium")) return "chromium";
  return "unknown_web";
};

let reporter: ClientDiagnosticsReporter;
reporter = new ClientDiagnosticsReporter(
  async (events) => {
    await ingestClientDiagnostics(reporter.wireBatch(events), { keepalive: true });
  },
  { clientVersion: "embedded", platform: webPlatform(), source: "web" },
);

const errorClassOf = (error: unknown): GeneratedClientObservation["errorClass"] => {
  if (error instanceof TypeError) return "type_error";
  if (error instanceof RangeError) return "range_error";
  if (error instanceof Error) return "error";
  return "unknown";
};

const installGlobalClientDiagnostics = () => {
  observeApiFailures(({ requestId, status }) => {
    reporter.record({ event: "client.api_failed", requestId, httpStatus: status });
  });
  window.addEventListener("error", (event) => {
    reporter.record({
      event: "client.unhandled_error",
      surface: "root",
      errorClass: errorClassOf(event.error),
    });
  });
  window.addEventListener("unhandledrejection", () => {
    reporter.record({ event: "client.unhandled_error", surface: "root", errorClass: "promise_rejection" });
  });
};

export type { ClientObservation, SendBatch };
export { ClientDiagnosticsReporter, installGlobalClientDiagnostics, reporter as clientDiagnostics };
