export type { ClientDestination, ClientNavigationProps } from "./src/client-navigation";
export { ClientNavigation, clientBackDestination, clientDestinationLabel } from "./src/client-navigation";
export { ClientPlatformProof } from "./src/client-platform-proof";
export type { ClientShellProps } from "./src/client-shell";
export { ClientShell } from "./src/client-shell";
export type { DeviceDisconnectActionProps } from "./src/device-disconnect";
export { DeviceDisconnectAction } from "./src/device-disconnect";
export type { FocusableTargetHandle, FocusTargetRegistry } from "./src/focus-target";
export type {
  GuideArtworkRenderer,
  GuideChannelWindow,
  GuideExperienceProps,
  GuideFilter,
  GuideFilterOption,
  GuideFocusTarget,
  GuideJourneyProps,
  GuideLogoRenderer,
  GuideSurfaceProps,
  GuideUnavailableState,
} from "./src/guide";
export { GuideExperience, GuideJourney, GuideSurface } from "./src/guide";
export type {
  ChannelIdentityData,
  ChannelIdentityProps,
  ProgrammeIdentityData,
  ProgrammeIdentityProps,
} from "./src/identity";
export { ChannelIdentity, ProgrammeIdentity } from "./src/identity";
export type { ModalOverlayProps, OverlayAction, TransientOverlayProps } from "./src/overlay";
export { ModalOverlay, TransientOverlay } from "./src/overlay";
export type { PairingShellProps } from "./src/pairing-shell";
export { PairingShell } from "./src/pairing-shell";
export type { ProgrammeCardData, ProgrammeCardProps } from "./src/programme-card";
export { ProgrammeCard } from "./src/programme-card";
export type { StatePanelAction, StatePanelKind, StatePanelProps } from "./src/state-panel";
export { StatePanel } from "./src/state-panel";
export type {
  SurfChannelData,
  SurfGroupData,
  SurfGroupKind,
  SurfGroupsArgs,
  SurfJourneyProps,
  SurfProgrammeData,
  SurfRailProps,
  SurfSelection,
} from "./src/surf-rail";
export {
  restoreSurfSelection,
  SurfJourney,
  SurfRail,
  surfGroupsFromGuide,
  surfPreviousChannel,
  watchingScheduleFromGuide,
} from "./src/surf-rail";
export type {
  ChannelNumberEntry,
  WatchingProgrammeData,
  WatchingScheduleData,
  WatchingSurfaceProps,
} from "./src/watching-surface";
export { WatchingSurface } from "./src/watching-surface";
