interface FocusableTargetHandle {
  focus?: () => void;
}

interface FocusTargetRegistry<TTarget> {
  register: (target: TTarget, handle: FocusableTargetHandle | null) => void;
  request: (target: TTarget) => void;
}

export type { FocusableTargetHandle, FocusTargetRegistry };
