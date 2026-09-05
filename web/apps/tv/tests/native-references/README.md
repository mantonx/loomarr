# TV native references

These images are full-screen captures from the embedded React Native Storybook. They cover the
Shield parity states at the same physical layouts and logical viewport used by the Kotlin
references: 1920×1080 at 320 dpi and 3840×2160 at 640 dpi.

Run `pnpm --filter @loomarr/tv references:capture` with exactly one matching Android TV emulator
running. The capture script detects the emulator's native framebuffer and writes that layout only;
run it once with the Android Studio **Television (1080p)** profile and once with **Television (4K)**.
It deliberately rejects `wm size` overrides because they change Android's logical display without
changing the emulator screenshot framebuffer.

The checked-in images are parity evidence, not a snapshot-update shortcut. Compare changed primary
surfaces against `android/app/src/test/screenshots/` and review every loading, empty, error, and
focused state before accepting new references.
