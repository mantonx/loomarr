import type { SettingEntry } from "@loomarr/api/models/settingEntry";
import { createFileRoute, Link } from "@tanstack/react-router";
import { NavTabs } from "@/components/ui/nav-tabs";
import { SettingsEditsProvider } from "@/settings/settings-edits";
import { SettingsPage } from "@/settings/settings-page";
import { SettingsSaveBarHost } from "@/settings/settings-save-bar-host";
import { useSettingsEntries } from "@/settings/use-settings-entries";

const settingValue = (entries: SettingEntry[], key: string): string =>
  entries.find((entry) => entry.key === key)?.value ?? "";

// The language stage already skips with these same configuration facts. Mirror them at the
// decision point so `en` cannot look active while every clip is actually passing unchecked.
const languageUnavailableReason = (entries: SettingEntry[]): string | undefined => {
  const provider = settingValue(entries, "filler.language_provider") || "whisper";
  if (provider === "hosted") {
    if (settingValue(entries, "llm.url") === "") {
      return "Language filtering is off because the hosted AI service address is not configured. Set it under Settings → AI.";
    }
    if (settingValue(entries, "llm.model") === "") {
      return "Language filtering is off because the hosted language model is not configured. Set it under Settings → AI.";
    }
  } else {
    if (settingValue(entries, "ingest.whisper_path") === "") {
      return "Language filtering is off because the local language engine is not configured. Set the whisper executable under Processing tools.";
    }
    if (settingValue(entries, "filler.language_model") === "") {
      return "Language filtering is off because no multilingual detection model is configured. Add one under Settings → AI.";
    }
  }
  if (settingValue(entries, "playout.ffmpeg_path") === "") {
    return "Language filtering is off because audio extraction is not configured. Set the ffmpeg executable under System → Playback.";
  }
  return undefined;
};

const FillerOperations = () => {
  const entries = useSettingsEntries();
  const languageReason = languageUnavailableReason(entries);
  return (
    <SettingsPage
      title="Filler settings"
      description="Where clips arrive, how breaks are assembled, and the limits that keep background processing bounded. Per-channel frequency and clip matching live on each channel's Filler page."
      entries={entries}
      blocks={[
        {
          group: "filler",
          title: "Clip folders",
          description: "Where clips are stored and how files dropped onto this machine enter the catalog.",
          keys: ["filler.dir", "filler.watch_dir", "filler.source.folder.enabled", "filler.sync_every"],
        },
        {
          group: "filler",
          title: "Automatic downloads",
          description: "How often enabled sources are checked. Safety limits stay available under Advanced.",
          keys: [
            "filler.fetch.every",
            "filler.fetch.max_per_run",
            "filler.fetch.max_catalog_clips",
            "filler.fetch.max_disk_gb",
          ],
        },
        {
          group: "filler",
          title: "Break assembly",
          description:
            "Default break length and clip density. A channel can override its length; frequency stays under Settings → Defaults.",
          keys: ["filler.break_duration", "filler.pod_max"],
        },
        {
          group: "filler",
          title: "Clip review",
          description: "Choose which background checks may identify, split, or set aside incoming clips.",
          keys: [
            "filler.ai_tagging",
            "filler.transcribe.enabled",
            "filler.vision.enabled",
            "filler.autosplit.enabled",
            "filler.autosplit.min_confidence",
            "filler.autosplit.max_duration",
            "filler.reject.unidentified",
          ],
        },
        {
          group: "filler",
          title: "Clip eligibility and sound",
          disabledReasons: languageReason ? { "filler.language": languageReason } : undefined,
          keys: [
            "filler.cooldown_seconds",
            "filler.min_quality",
            "filler.weight",
            "filler.min_duration",
            "filler.split.review_window",
            "filler.min_clip_duration",
            "filler.max_clip_duration",
            "filler.target_lufs",
            "filler.language",
          ],
        },
        {
          group: "filler",
          title: "Pipeline limits",
          description: "Per-pass budgets that keep background work from taking over the machine.",
          keys: [
            "filler.pipeline.max_clips",
            "filler.transcode.max_per_run",
            "filler.pipeline.max_whisper",
            "filler.pipeline.max_vision",
            "filler.pipeline.max_split_vision",
            "filler.pipeline.max_splits",
          ],
        },
        {
          group: "filler",
          title: "Processing tools",
          description:
            "Executable and model paths for unusual source installs. The container supplies these.",
          keys: [
            "ingest.ytdlp_path",
            "ingest.ffmpeg_path",
            "ingest.timeout",
            "ingest.whisper_path",
            "ingest.whisper_model",
          ],
        },
      ]}
    />
  );
};

const FillerSettingsScreen = () => (
  <SettingsEditsProvider>
    <div className="flex h-full min-h-0 flex-col">
      <NavTabs
        label="Filler sections"
        linkComponent={Link}
        className="bg-background px-6 pt-2"
        activeId="manage"
        tabs={[
          { id: "overview", label: "Overview", to: "/filler" },
          { id: "sources", label: "Sources", to: "/filler/sources" },
          { id: "incoming", label: "Incoming", to: "/filler/incoming" },
          { id: "library", label: "Library", to: "/filler/library" },
          { id: "manage", label: "Manage", to: "/filler/manage" },
        ]}
      />
      <div className="min-h-0 flex-1 overflow-hidden">
        <FillerOperations />
      </div>
      <SettingsSaveBarHost />
    </div>
  </SettingsEditsProvider>
);

const Route = createFileRoute("/_authed/filler/settings")({
  component: FillerSettingsScreen,
});

export { Route };
