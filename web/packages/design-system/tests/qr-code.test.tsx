import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { LoomarrProvider, QrCode } from "../index";

describe("Loomarr QR code", () => {
  it("renders an accessible, high-contrast machine-readable pairing payload", () => {
    const markup = renderToStaticMarkup(
      <LoomarrProvider>
        <QrCode size={180} value="https://loomarr.media/pair?code=LOOM-ARRR" />
      </LoomarrProvider>,
    );

    expect(markup).toContain('aria-label="Pair Loomarr with this QR code"');
    expect(markup).toContain('role="img"');
    expect(markup).toContain('<path d="');
    expect(markup).toContain("#0B0C0E");
    expect(markup).toContain("#F7F8FA");
    expect(markup).toContain('aria-hidden="true"');
    expect(markup).toContain("#FFB020");
    expect(markup).toContain("#8B93A3");
  });

  it("can omit the centre mark when a product surface explicitly requests it", () => {
    const markup = renderToStaticMarkup(
      <LoomarrProvider>
        <QrCode showBrandMark={false} value="https://loomarr.media/pair?code=LOOM-ARRR" />
      </LoomarrProvider>,
    );

    expect(markup).not.toContain('aria-hidden="true"');
  });
});
