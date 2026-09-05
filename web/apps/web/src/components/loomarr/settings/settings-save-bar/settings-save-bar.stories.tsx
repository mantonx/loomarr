import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect } from "react";
import { Toaster, toast } from "sonner";
import { widthFrame } from "@/test/story-utils";
import { SettingsSaveBar } from "./settings-save-bar";

const noop = () => {};

// Sonarr's sticky save bar (config-design §5): explicit per-page saving, because
// connection settings change together and half-saved pairs are a footgun. It renders
// nothing at all when the page is clean.
const meta = {
  title: "Settings/SettingsSaveBar",
  component: SettingsSaveBar,
  args: { dirtyCount: 2, onSave: noop, onDiscard: noop },
  decorators: [widthFrame(560)],
} satisfies Meta<typeof SettingsSaveBar>;

type Story = StoryObj<typeof meta>;

const Dirty: Story = {};
const SingleChange: Story = { args: { dirtyCount: 1 } };
const Saving: Story = { args: { saving: true } };

const PersistentHealthNoticeFixture = () => {
  useEffect(() => {
    const id = toast.warning("Loomarr is running with warnings", {
      description: "v1.2.3 · 1 check needs attention",
      duration: Number.POSITIVE_INFINITY,
      action: { label: "View health", onClick: noop },
      closeButton: true,
    });
    return () => {
      toast.dismiss(id);
    };
  }, []);

  return (
    <main className="relative min-h-screen">
      <SettingsSaveBar dirtyCount={2} onSave={noop} onDiscard={noop} className="fixed inset-x-0 bottom-0" />
      <Toaster theme="dark" position="top-right" richColors />
    </main>
  );
};

const PersistentHealthNotice: Story = {
  render: () => <PersistentHealthNoticeFixture />,
  parameters: { layout: "fullscreen" },
};

export default meta;
export { Dirty, PersistentHealthNotice, Saving, SingleChange };
