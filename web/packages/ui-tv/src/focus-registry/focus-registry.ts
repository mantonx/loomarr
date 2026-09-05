import type {
  FocusableTargetHandle,
  FocusTargetRegistry,
  GuideFocusTarget,
  SurfSelection,
} from "@loomarr/ui";

class TvFocusRegistry<TTarget> implements FocusTargetRegistry<TTarget> {
  private readonly handles = new Map<string, FocusableTargetHandle>();
  private pendingKey?: string;

  constructor(private readonly keyFor: (target: TTarget) => string) {}

  register = (target: TTarget, handle: FocusableTargetHandle | null) => {
    const key = this.keyFor(target);
    if (!handle) {
      this.handles.delete(key);
      return;
    }
    this.handles.set(key, handle);
    if (this.pendingKey === key) {
      this.pendingKey = undefined;
      handle.focus?.();
    }
  };

  request = (target: TTarget) => {
    const key = this.keyFor(target);
    const handle = this.handles.get(key);
    if (handle) {
      this.pendingKey = undefined;
      handle.focus?.();
      return;
    }
    this.pendingKey = key;
  };
}

const guideFocusTargetKey = (target: GuideFocusTarget): string =>
  target.kind === "filter"
    ? `filter:${target.filter}`
    : `airing:${target.selection.channelId}:${target.selection.scheduleBlockId}`;

const surfFocusTargetKey = (target: SurfSelection): string => `${target.group}:${target.channelId}`;

const createTvGuideFocusRegistry = () => new TvFocusRegistry<GuideFocusTarget>(guideFocusTargetKey);
const createTvSurfFocusRegistry = () => new TvFocusRegistry<SurfSelection>(surfFocusTargetKey);

export { createTvGuideFocusRegistry, createTvSurfFocusRegistry, TvFocusRegistry };
