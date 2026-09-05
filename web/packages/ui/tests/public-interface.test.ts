import { describe, expect, it } from "vitest";

import {
  ChannelIdentity,
  ClientNavigation,
  ClientPlatformProof,
  ClientShell,
  clientBackDestination,
  DeviceDisconnectAction,
  GuideExperience,
  GuideJourney,
  GuideSurface,
  ModalOverlay,
  PairingShell,
  ProgrammeCard,
  ProgrammeIdentity,
  StatePanel,
  SurfJourney,
  SurfRail,
  TransientOverlay,
  WatchingSurface,
} from "../index";

describe("ui public interface", () => {
  it("exports the shared scaffold surface", () => {
    expect(ClientPlatformProof).toBeTypeOf("function");
    expect(ClientShell).toBeTypeOf("function");
    expect(DeviceDisconnectAction).toBeTypeOf("function");
    expect(PairingShell).toBeTypeOf("function");
    expect(ProgrammeCard).toBeTypeOf("function");
    expect(ChannelIdentity).toBeTypeOf("function");
    expect(ClientNavigation).toBeTypeOf("function");
    expect(GuideSurface).toBeTypeOf("function");
    expect(GuideExperience).toBeTypeOf("function");
    expect(GuideJourney).toBeTypeOf("function");
    expect(clientBackDestination).toBeTypeOf("function");
    expect(ProgrammeIdentity).toBeTypeOf("function");
    expect(ModalOverlay).toBeTypeOf("function");
    expect(TransientOverlay).toBeTypeOf("function");
    expect(StatePanel).toBeTypeOf("function");
    expect(SurfJourney).toBeTypeOf("function");
    expect(SurfRail).toBeTypeOf("function");
    expect(WatchingSurface).toBeTypeOf("function");
  });
});
