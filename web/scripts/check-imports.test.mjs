import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  findCatalogImports,
  findFrameworkImports,
  findProductStateDeclarations,
  findSharedImplementationImports,
} from "./check-imports.mjs";

describe("findCatalogImports", () => {
  it("rejects value, type-only, re-export, and dynamic catalog dependencies", () => {
    const source = `
      import { Button } from "@/components/ui";
      import type { ChannelDTO } from "@loomarr/api";
      export * from "@/channels";
      const core = import("@loomarr/core");
    `;

    assert.deepEqual(
      findCatalogImports(source).map(({ importPath }) => importPath),
      ["@/components/ui", "@loomarr/api", "@/channels", "@loomarr/core"],
    );
  });

  it("accepts imports through specific public interfaces", () => {
    const source = `
      import { Button } from "@/components/ui/button";
      import type { ChannelDTO } from "@loomarr/api/models/channelDTO";
      import * as channelsApi from "@loomarr/api/endpoints/channels";
      import type { EventHandlers } from "@loomarr/core/events";
    `;

    assert.deepEqual(findCatalogImports(source), []);
  });
});

describe("findFrameworkImports", () => {
  it("rejects direct Tamagui imports from product modules", () => {
    const source = `
      import { View } from "@tamagui/core";
      export { Button } from "tamagui";
      const runtime = import("@tamagui/web");
    `;

    assert.deepEqual(
      findFrameworkImports(source).map(({ importPath }) => importPath),
      ["@tamagui/core", "tamagui", "@tamagui/web"],
    );
  });

  it("accepts Loomarr-owned design-system imports", () => {
    assert.deepEqual(findFrameworkImports('import { Screen } from "@loomarr/design-system";'), []);
  });
});

describe("findSharedImplementationImports", () => {
  it("rejects static, type-only, re-export, and dynamic imports of package internals", () => {
    const source = `
      import { Guide } from "@loomarr/ui/src/guide/guide";
      import type { PlayerSnapshot } from "@loomarr/player/src/player-controller/player-controller.type";
      export * from "@loomarr/core/tests/guide-fixture";
      const tv = import("@loomarr/ui-tv/src/guide-navigation");
    `;

    assert.deepEqual(
      findSharedImplementationImports(source).map(({ importPath }) => importPath),
      [
        "@loomarr/ui/src/guide/guide",
        "@loomarr/player/src/player-controller/player-controller.type",
        "@loomarr/core/tests/guide-fixture",
        "@loomarr/ui-tv/src/guide-navigation",
      ],
    );
  });

  it("accepts focused public entry points", () => {
    const source = `
      import { GuideJourney } from "@loomarr/ui";
      import type { GuideSnapshot } from "@loomarr/core/guide";
      import { createGuideController } from "@loomarr/core/guide";
      import { reduceTvWatchingRemote } from "@loomarr/ui-tv";
    `;

    assert.deepEqual(findSharedImplementationImports(source), []);
  });
});

describe("findProductStateDeclarations", () => {
  it("rejects competing Player, Guide, and Surf state models and behavior in app roots", () => {
    const source = `
      interface LocalPlayerState { status: string }
      type GuideSnapshot = { selected: string };
      class SurfNavigation {}
      interface LocalSurfStore {}
      const createPlayerController = () => ({});
      function reduceGuideState() {}
      const restoreSurfSelection = () => undefined;
    `;

    assert.deepEqual(
      findProductStateDeclarations(source).map(({ name }) => name),
      [
        "LocalPlayerState",
        "GuideSnapshot",
        "SurfNavigation",
        "LocalSurfStore",
        "createPlayerController",
        "reduceGuideState",
        "restoreSurfSelection",
      ],
    );
  });

  it("accepts composition variables, adapters, and unrelated local UI state", () => {
    const source = `
      const guideController = createGuideController({ source });
      const [active, setActive] = useState("watching");
      const createPairingStore = () => store;
      function renderGuide() { return <GuideJourney controller={guideController} />; }
    `;

    assert.deepEqual(findProductStateDeclarations(source), []);
  });
});
